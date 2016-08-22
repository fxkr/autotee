package autotee

import (
	"os"

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
	return &ExclusiveScreenService{BaseScreenService{name: name}}
}

func NewSharedScreenService(name string) ScreenService {
	return &SharedScreenService{BaseScreenService{name: name}}
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
