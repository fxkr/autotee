package rtmptee

import (
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/deckarep/golang-set"
	"github.com/pwaller/barrier"
)

// SinkSet starts and supervises multiple sinks.
type SinkSet struct {
	log *log.Entry

	commands map[string]CmdData

	c <-chan *BufPoolElem

	config *Config

	addSink    chan *Sink // blocking
	removeSink chan *Sink // blocking

	runExited chan struct{}

	quitBarrier barrier.Barrier
	quitWait    sync.WaitGroup
}

func NewSinkSet(commands map[string]CmdData, buffers <-chan *BufPoolElem, config *Config, entry *log.Entry) *SinkSet {
	return &SinkSet{
		log: entry,

		commands: commands,

		c: buffers,

		config: config,

		addSink:    make(chan *Sink),
		removeSink: make(chan *Sink),

		runExited: make(chan struct{}),
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
	ss.quitBarrier.Fall()
	ss.quitWait.Wait()
}

// goStartSink starts a sink, retrying periodically until it succeeds.
func (ss *SinkSet) goStartSink(name string, command CmdData) {
	ss.quitWait.Add(1)
	go func() {
		defer ss.quitWait.Done()

		for {

			s := NewSink(ss.log, name, command, &ss.config.SinkBuffer)

			// Try to start process
			if err := s.Start(); err != nil {
				s.log.WithError(err).Warn("Sink failed to start")

				// Wait before trying again
				select {
				case <-time.After(3 * time.Second):
					continue
				case <-ss.quitBarrier.Barrier():
					return
				}
			}

			// Give it to goRun
			select {
			case ss.addSink <- s:
			case <-ss.quitBarrier.Barrier():
			}

			// Wait till it dies
			select {
			case <-s.DeathBarrier():
			case <-ss.quitBarrier.Barrier():
			}

			// Take it back
			select {
			case ss.removeSink <- s:
			case <-ss.runExited:
			}

			// Wait till its really dead
			s.Stop()

			// Wait before respawning
			select {
			case <-time.After(ss.config.Times.SinkRestartDelay):
				continue
			case <-ss.quitBarrier.Barrier():
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
					// (Instead, wait for quitBarrier to fall.)
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
			case <-ss.quitBarrier.Barrier():

				// We consider all our sinks released
				close(ss.runExited)

				return
			}
		}
	}()
}
