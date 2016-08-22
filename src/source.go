package autotee

import (
	"fmt"
	"io"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/juju/errors"
	"github.com/pwaller/barrier"
	"github.com/rcrowley/go-metrics"
	"golang.org/x/net/context"
)

type Source struct {
	ctx context.Context

	log  *log.Entry
	name string

	command CmdData

	screen *Screen

	c chan *BufPoolElem

	bufpool *BufPool

	cmd    *Cmd
	stdout io.Reader

	// Falls when the process dies.
	deathBarrier barrier.Barrier

	// Continues when all goroutines are exiting.
	quitWait sync.WaitGroup

	cancel context.CancelFunc
}

func NewSource(ctx context.Context, name string, command CmdData, config *Config, entry *log.Entry, bufpool *BufPool, screen *Screen) *Source {
	srcCtx, cancel := context.WithCancel(ctx)

	return &Source{
		ctx: srcCtx,

		log:  entry,
		name: name,

		command: command,
		screen:  screen,

		c: make(chan *BufPoolElem),

		bufpool: bufpool,

		cancel: cancel,
	}
}

func (s *Source) Start() (err error) {

	// Note: logging here should be consistent with logging in Sink.Start()
	s.log.Debug("Starting source")

	// Start source
	s.cmd = s.command.NewCmd()
	s.stdout, err = s.cmd.StdoutPipe()
	if err != nil {
		s.log.WithError(err).Info("Failed to create pipe")
		return errors.Trace(err)
	}
	s.cmd.SetStderr(s.screen.File)
	err = s.cmd.Start()
	if err != nil {
		s.log.WithError(err).Info("Failed to start process")
		return errors.Trace(err)
	}

	// Begin reading
	s.goRun()

	s.log.WithFields(log.Fields{
		"screen": s.screen.Name,
	}).Info("Source started")
	return nil
}

func (s *Source) Channel() <-chan *BufPoolElem {
	return s.c
}

// Doesn't block.
func (s *Source) Kill() {
	s.cancel()
}

func (s *Source) DeathBarrier() <-chan struct{} {
	return s.deathBarrier.Barrier()
}

// Blocks.
func (s *Source) Stop() {
	s.cancel()
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
			<-s.ctx.Done()
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
			case <-s.ctx.Done():
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
				case <-s.ctx.Done():
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
