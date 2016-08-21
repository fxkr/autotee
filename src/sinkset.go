package autotee

import (
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/deckarep/golang-set"
	"github.com/pwaller/barrier"
	"golang.org/x/net/context"
)

// SinkSet starts and supervises multiple sinks.
type SinkSet struct {
	ctx context.Context
	log *log.Entry

	commands map[string]SinkCmdData

	c <-chan *BufPoolElem

	config *Config

	addSink    chan *Sink // blocking
	removeSink chan *Sink // blocking

	runExited chan struct{}

	quitWait sync.WaitGroup

	anySinkDied barrier.Barrier

	cancel context.CancelFunc
}

type SinkCmdData struct {
	Screens ScreenService
	Command CmdData
}

func NewSinkSet(ctx context.Context, commands map[string]SinkCmdData, buffers <-chan *BufPoolElem, config *Config, entry *log.Entry) *SinkSet {
	sinkSetCtx, cancel := context.WithCancel(ctx)

	return &SinkSet{
		ctx: sinkSetCtx,
		log: entry,

		commands: commands,

		c: buffers,

		config: config,

		addSink:    make(chan *Sink),
		removeSink: make(chan *Sink),

		runExited: make(chan struct{}),

		cancel: cancel,
	}
}

// Start makes the SinkSet begin managing source and sinks and forwarding data.
//
// Must only be called once.
// Does not block.
func (ss *SinkSet) Start() {
	ss.goRun()
	for name, command := range ss.commands {
		ss.goStartSink(name, command)
	}
}

// Stop ends all processes and goroutines.
//
// Idempotent.
// Blocks until all processes have been killed and goroutines are exiting.
func (ss *SinkSet) Stop() {
	ss.cancel()
	ss.quitWait.Wait()
}

// goStartSink starts a sink, retrying periodically until it succeeds.
func (ss *SinkSet) goStartSink(name string, command SinkCmdData) {
	ss.quitWait.Add(1)
	go func() {
		defer ss.quitWait.Done()

		for {

			// Get a screen for the new process
			screen, err := command.Screens.Screen()
			if err != nil {
				ss.log.WithError(err).Warn("Failed to start screen")

				// Wait before trying again
				select {
				case <-time.After(ss.config.Times.SinkRestartDelay):
					continue
				case <-ss.ctx.Done():
					return
				}
			}

			s := NewSink(ss.ctx, ss.log, name, command.Command, &ss.config.SinkBuffer, screen)

			// Try to start process
			if err := s.Start(); err != nil {
				s.log.WithError(err).Warn("Sink failed to start")
				command.Screens.Done()

				// Wait before trying again
				select {
				case <-time.After(ss.config.Times.SinkRestartDelay):
					continue
				case <-ss.ctx.Done():
					return
				}
			}

			// Give it to goRun
			select {
			case ss.addSink <- s:
			case <-ss.ctx.Done():
			}

			// Wait till it dies
			select {
			case <-s.DeathBarrier():
			case <-ss.ctx.Done():
			}

			ss.anySinkDied.Fall()

			// Take it back
			select {
			case ss.removeSink <- s:
			case <-ss.runExited:
			}

			// Wait till its really dead
			s.Stop()

			command.Screens.Done()

			// Wait before respawning
			select {
			case <-time.After(ss.config.Times.SinkRestartDelay):
				continue
			case <-ss.ctx.Done():
				return
			}
		}
	}()
}

// goRun delivers incoming buffers to sinks.
func (ss *SinkSet) goRun() {
	ss.quitWait.Add(1)
	go func() {
		defer ss.quitWait.Done()

		sinks := mapset.NewSet()
		for {
			select {

			// New sinks from goRun
			case s := <-ss.addSink:
				sinks.Add(s)

			// Sinks that have died (on their own)
			case s := <-ss.removeSink:
				sinks.Remove(s)

			// New buffer with bytes
			case buf, more := <-ss.c:

				// Channel closed?
				if !more {
					// Don't read from channel again (<-nil blocks).
					// (Instead, wait for ctx to get canceled.)
					ss.c = nil
					continue
				}

				buf.Acquire(int32(sinks.Cardinality()))
				for sink := range sinks.Iter() {
					sink := sink.(*Sink)

					select {

					// Send to sink
					case sink.Channel() <- buf:

					// Sink stalling?
					default:
						sink.log.Warn("Sink stalled")
						buf.Free() // the sinks ref
						sinks.Remove(sink)
						sink.Kill()
					}
				}
				buf.Free() // our own ref

			// SinkSet is quitting
			case <-ss.ctx.Done():

				// We consider all our sinks released
				close(ss.runExited)

				return
			}
		}
	}()
}

func (ss *SinkSet) AnySinkDied() <-chan struct{} {
	return ss.anySinkDied.Barrier()
}
