package autotee

import (
	"os"
	"os/user"

	log "github.com/Sirupsen/logrus"
	"github.com/juju/errors"
	"github.com/kr/pty"
)

type Screen struct {
	Name string
	File *os.File
}

type ScreenService interface {
	Screen() (*Screen, error)
	Done() error
	Stop()
}

type BaseScreenService struct {
	name     string
	cmd      *Cmd
	pty, tty *os.File

	screen    *Screen
	hasScreen bool
}

type ExclusiveScreenService struct {
	BaseScreenService
}

type SharedScreenService struct {
	BaseScreenService
}

func NewExclusiveScreenService(name string) ScreenService {
	return &ExclusiveScreenService{BaseScreenService{name: safeScreenName(name)}}
}

func NewSharedScreenService(name string) ScreenService {
	return &SharedScreenService{BaseScreenService{name: safeScreenName(name)}}
}

func (s *BaseScreenService) spawn() error {
	var err error

	// Create PTY/TTY pair
	s.pty, s.tty, err = pty.Open()
	if err != nil {
		return errors.Annotate(err, "Failed to create PTY")
	}

	// Attach screen to PTY
	s.cmd = Command("screen", "-DmUS", s.name, s.tty.Name())
	s.cmd.SetStdin(s.pty)
	err = s.cmd.Start()
	if err != nil {
		return errors.Annotate(err, "Failed to start screen")
	}

	s.hasScreen = true
	s.screen = &Screen{s.name, s.pty}
	return nil
}

func (s *BaseScreenService) getScreen() *Screen {
	return &Screen{s.name, s.tty}
}

func (s *SharedScreenService) Stop() {
	if s.hasScreen {
		s.hasScreen = false
		s.pty.Close()
		s.tty.Close()

		err := s.cmd.End()
		if !IsExit(err) {
			log.WithError(err).Warn("Failed to stop screen")
		}
	}
}

func (s *ExclusiveScreenService) Stop() {
	if s.hasScreen {
		err := s.Done()
		if !IsExit(err) {
			log.WithError(err).Warn("Failed to stop screen")
		}
	}
}

func (s *SharedScreenService) Screen() (*Screen, error) {
	if !s.hasScreen {
		if err := s.spawn(); err != nil {
			return nil, err
		}
	}
	return s.screen, nil
}

func (s *ExclusiveScreenService) Screen() (*Screen, error) {
	if s.hasScreen {
		log.Warn("Bug: Done() not called")
		s.Done()
	}
	s.spawn()
	return s.screen, nil
}

func (s *SharedScreenService) Done() error {
	// do nothing
	return nil
}

func (s *ExclusiveScreenService) Done() error {
	s.hasScreen = false
	s.pty.Close()
	s.tty.Close()
	return s.cmd.End()
}

// safeScreenName shortens a string so it can be used as a gnu screen instance name ("-S").

// So how long can a screen name be?
//
// Screen creates sockets like /var/run/screen/S-$user/$pid.$name
// According to unix(7), socket paths can not be longer than 108 characters.
// This includes 1 character for null termination.
// The directory part is 16 bytes long. (By default, anyway, it's configurable...)
// Usernames can have up to 32 bytes.
// But since we really need the space, we try to look up the actual name.
// PIDs in decimal notation have up to 5 bytes.
// Screen also adds a '.' character.
func safeScreenName(screenName string) string {

	userNameLength := 32
	if currentUser, err := user.Current(); err == nil {
		userNameLength = len(currentUser.Name)
	}

	maxSafeishScreenNameLength := 108 - 1 - userNameLength - 5 - 1
	if len(screenName) > maxSafeishScreenNameLength {
		screenName = screenName[:maxSafeishScreenNameLength]
	}

	return screenName
}
