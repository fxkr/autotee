package autotee

import (
	"io"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/juju/errors"
	"github.com/pwaller/barrier"
	"golang.org/x/net/context"
)

type Sink struct {
	ctx context.Context
	log *log.Entry

	command CmdData

	screen *Screen

	c chan *BufPoolElem

	cmd   *Cmd
	stdin io.Writer

	// Falls when the process dies.
	deathBarrier barrier.Barrier

	// Continues when all goroutines are exiting.
	quitWait sync.WaitGroup

	cancel context.CancelFunc
}

func NewSink(ctx context.Context, entry *log.Entry, name string, command CmdData, config *BufferConfig, screen *Screen) *Sink {
	sinkCtx, cancel := context.WithCancel(ctx)

	return &Sink{
		ctx: sinkCtx,

		log: entry.WithFields(log.Fields{"sink": name}),

		command: command,

		screen: screen,

		c: make(chan *BufPoolElem, config.BufferCount),

		cancel: cancel,
	}
}

// Must only be called once.
// Blocks.
func (s *Sink) Start() (err error) {

	// Note: logging here should be consistent with logging in Source.Start()
	s.log.Debug("Starting sink")

	// Start sink
	s.cmd = s.command.NewCmd()
	s.stdin, err = s.cmd.StdinPipe()
	if err != nil {
		return errors.Annotate(err, "Failed to create pipe")
	}
	s.cmd.SetStderr(s.screen.File)
	err = s.cmd.Start()
	if err != nil {
		return errors.Annotate(err, "Failed to start process")
	}

	// Begin reading
	s.goRun()

	s.log.WithFields(log.Fields{
		"screen": s.screen.Name,
		"pid":    s.cmd.Pid(),
	}).Info("Sink started")

	return nil
}

func (s *Sink) Channel() chan<- *BufPoolElem {
	return s.c
}

// Doesn't block.
func (s *Sink) Kill() {
	s.cancel()
}

func (s *Sink) DeathBarrier() <-chan struct{} {
	return s.deathBarrier.Barrier()
}

// Blocks.
func (s *Sink) Stop() {
	s.cancel()
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
			<-s.ctx.Done()
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

			case <-s.ctx.Done():
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
			case <-s.ctx.Done():
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
