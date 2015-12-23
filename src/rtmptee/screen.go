package rtmptee

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

type ExclusiveScreenService struct {
	name      string
	cmd       *Cmd
	pty, tty  *os.File
	hasScreen bool
}

func NewExclusiveScreenService(name string) ScreenService {
	return &ExclusiveScreenService{name: name}
}

func (s *ExclusiveScreenService) Screen() (*Screen, error) {
	var err error

	if s.hasScreen {
		log.Warn("Bug: Done() wasn't called")
		s.Done()
	}

	// Create PTY/TTY pair
	s.pty, s.tty, err = pty.Open()
	if err != nil {
		return nil, errors.Annotate(err, "Failed to create PTY")
	}

	// Attach screen to PTY
	s.cmd = Command("screen", "-DmUS", s.name, s.tty.Name())
	s.cmd.SetStdin(s.pty)
	err = s.cmd.Start()
	if err != nil {
		return nil, errors.Annotate(err, "Failed to start screen")
	}

	s.hasScreen = true
	return &Screen{s.name, s.pty}, nil
}

func (s *ExclusiveScreenService) Done() error {
	s.hasScreen = false
	s.pty.Close()
	s.tty.Close()
	return s.cmd.End()
}

func (s *ExclusiveScreenService) Stop() {
	if s.hasScreen {
		s.hasScreen = false
		err := s.Done()
		log.WithError(err).Warn("Failed to stop screen")
	}
}
