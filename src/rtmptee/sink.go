package rtmptee

import (
	"fmt"
	"io"
	"os"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/juju/errors"
	"github.com/kr/pty"
	"github.com/pwaller/barrier"
)

type Sink struct {
	log *log.Entry

	command CmdData

	c chan *BufPoolElem

	cmd      *Cmd
	pty, tty *os.File
	stdin    io.Writer

	// Falls when the process dies.
	deathBarrier barrier.Barrier

	// Falls when Stop() is called.
	quitBarrier barrier.Barrier

	// Continues when all goroutines are exiting.
	quitWait sync.WaitGroup
}

func NewSink(entry *log.Entry, name string, command CmdData, config *BufferConfig) *Sink {
	return &Sink{
		log: entry.WithFields(log.Fields{"sink": name}),

		command: command,

		c: make(chan *BufPoolElem, config.BufferCount),
	}
}

// Must only be called once.
// Blocks.
func (s *Sink) Start() (err error) {
	s.log.Debug("Starting sink")

	// Create PTY/TTY pair
	s.pty, s.tty, err = pty.Open()
	if err != nil {
		return errors.Annotate(err, "Failed to create PTY")
	}

	// Important, without dead screens stay around sometimes
	defer s.pty.Close()
	defer s.tty.Close()

	// Start screen
	screenName := fmt.Sprintf("rtmptee.%d.sink", os.Getpid())
	screen := Command("screen", "-DmUS", screenName, s.tty.Name())
	screen.SetStdin(s.pty)
	err = screen.Start()
	if err != nil {
		return errors.Annotate(err, "Failed to start screen")
	}

	// Start sink
	s.cmd = s.command.NewCmd()
	s.stdin, err = s.cmd.StdinPipe()
	if err != nil {
		_ = screen.End()
		<-screen.WaitChannel()
		return errors.Annotate(err, "Failed to create pipe")
	}
	s.cmd.SetStderr(s.pty)
	err = s.cmd.Start()
	if err != nil {
		_ = screen.End()
		<-screen.WaitChannel()
		return errors.Annotate(err, "Failed to start process")
	}

	// When the sink dies, kill the screen too
	screen.EndWith(s.cmd)

	// Begin reading
	s.goRun()

	s.log.WithFields(log.Fields{
		"screen": screenName,
	}).Info("Sink started")

	return nil
}

func (s *Sink) Channel() chan<- *BufPoolElem {
	return s.c
}

// Doesn't block.
func (s *Sink) Kill() {
	s.quitBarrier.Fall()
}

func (s *Sink) DeathBarrier() <-chan struct{} {
	return s.deathBarrier.Barrier()
}

// Blocks.
func (s *Sink) Stop() {
	s.quitBarrier.Fall()
	s.quitWait.Wait()
}

func (s *Sink) goRun() {
	s.quitWait.Add(1)
	go func() {
		defer s.quitWait.Done()
		defer s.log.Debug("Sink stopped")

		// Make Write() interruptible
		killOnce := sync.Once{}
		s.quitWait.Add(1)
		go func() {
			defer s.quitWait.Done()
			<-s.quitBarrier.Barrier()
			// Important: we must never kill after wait
			killOnce.Do(func() { s.cmd.KillGroup() })
		}()

		// Process alive
		for running := true; running; {
			select {
			case buf, more := <-s.c:
				if !more {
					panic("Channel closed by wrong goroutine")
				}

				_, err := s.stdin.Write(buf.GetBuffer())
				buf.Free()
				if err != nil {
					s.log.WithError(err).Debug("Write failed")
					running = false
				}

			case <-s.quitBarrier.Barrier():
				running = false
			}
		}
		s.log.Debug("Sink dying")
		s.deathBarrier.Fall()

		// Process dead or dying, wait for Stop()
		for waitingForStop := true; waitingForStop; {
			select {
			case buf, more := <-s.c:
				if !more {
					panic("Channel closed by wrong goroutine")
				}
				buf.Free()
			case <-s.quitBarrier.Barrier():
				waitingForStop = false
				continue
			}
		}

		// Stop() was called
		killOnce.Do(func() { s.cmd.KillGroup() })
		<-s.cmd.WaitChannel()
		close(s.c)
		for buf := range s.c {
			buf.Free()
		}
	}()
}
