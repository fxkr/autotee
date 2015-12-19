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
	"github.com/rcrowley/go-metrics"
)

type Source struct {
	log  *log.Entry
	name string

	command CmdData

	c chan *BufPoolElem

	bufpool *BufPool

	cmd      *Cmd
	pty, tty *os.File
	stdout   io.Reader

	// Falls when the process dies.
	deathBarrier barrier.Barrier

	// Falls when Stop() is called.
	quitBarrier barrier.Barrier

	// Continues when all goroutines are exiting.
	quitWait sync.WaitGroup
}

func NewSource(name string, command CmdData, config *Config, entry *log.Entry, bufpool *BufPool) *Source {
	return &Source{
		log:  entry,
		name: name,

		command: command,

		c: make(chan *BufPoolElem),

		bufpool: bufpool,
	}
}

func (s *Source) Start() (err error) {
	s.log.Debug("Starting source")
	// TODO the logging in Source.Start() is not consistent with the logging in Sink.Start()

	// Create PTY/TTY pair
	s.pty, s.tty, err = pty.Open()
	if err != nil {
		return errors.Trace(err)
	}

	// Important, without dead screens stay around sometimes
	defer s.pty.Close()
	defer s.tty.Close()

	// Start screen
	screenName := fmt.Sprintf("rtmptee.%d.source", os.Getpid())
	screen := Command("screen", "-DmUS", screenName, s.tty.Name())
	screen.SetStdin(s.pty)
	err = screen.Start()
	if err != nil {
		s.log.WithError(err).Info("Failed to start screen")
		return errors.Trace(err)
	}

	// Start source
	s.cmd = s.command.NewCmd()
	s.stdout, err = s.cmd.StdoutPipe()
	if err != nil {
		s.log.WithError(err).Info("Failed to create pipe")
		_ = screen.End()
		<-screen.WaitChannel()
		return errors.Trace(err)
	}
	s.cmd.SetStderr(s.pty)
	err = s.cmd.Start()
	if err != nil {
		s.log.WithError(err).Info("Failed to start")
		_ = screen.End()
		<-screen.WaitChannel()
		return errors.Trace(err)
	}

	// When the sink dies, kill the screen
	screen.EndWith(s.cmd)

	// Begin reading
	s.goRun()

	s.log.WithFields(log.Fields{
		"screen": screenName,
	}).Info("Source started")
	return nil
}

func (s *Source) Channel() <-chan *BufPoolElem {
	return s.c
}

// Doesn't block.
func (s *Source) Kill() {
	s.quitBarrier.Fall()
}

func (s *Source) DeathBarrier() <-chan struct{} {
	return s.deathBarrier.Barrier()
}

// Blocks.
func (s *Source) Stop() {
	s.quitBarrier.Fall()
	s.quitWait.Wait()
}

func (s *Source) goRun() {
	s.quitWait.Add(1)
	go func() {
		defer s.quitWait.Done()
		defer s.log.Debug("Source stopped")

		throughputMetric := metrics.GetOrRegister(fmt.Sprintf("sink.%s.throughput", s.name), metrics.NewMeter()).(metrics.Meter)

		// Make Read() interruptible
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

			// Get a buffer
			var elem *BufPoolElem
			select {
			case elem = <-s.bufpool.C:
				elem.AcquireFirst()
			case <-s.quitBarrier.Barrier():
				running = false
				continue
			default:
				s.log.Error("Source out of buffer space")
				running = false
				continue
			}

			// Read bytes (blocking operation!)
			// FIXME read in another goroutine, receive timeout here
			n, err := s.stdout.Read(elem.GetBuffer())

			if n > 0 {
				elem.SetSize(n)

				select {
				case s.c <- elem:
					// Ok, buffer given away
					throughputMetric.Mark(int64(n))
				case <-s.quitBarrier.Barrier():
					elem.Free()
					running = false
					continue
				}
			} else {
				elem.Free()
			}

			if err == io.EOF || err != nil {
				s.log.WithError(err).Debug("Read failed")
				running = false
				continue
			}
		}
		s.log.Debug("Source dying")
		s.deathBarrier.Fall()

		// Process dead, wait for Stop()
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
