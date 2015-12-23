package rtmptee

import (
	"fmt"
	"os"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/pwaller/barrier"
)

type Flow struct {
	log    *log.Entry
	config *Config
	name   string

	sourceCmd CmdData
	sinkCmds  map[string]CmdData

	quitBarrier barrier.Barrier
	quitWait    sync.WaitGroup
}

func NewFlow(name string, config *Config, sourceCmd CmdData, sinkCmds map[string]CmdData, entry *log.Entry) *Flow {
	return &Flow{
		log:    entry,
		config: config,
		name:   name,

		sourceCmd: sourceCmd,
		sinkCmds:  sinkCmds,
	}
}

// Must only be called once.
// Does not block.
func (f *Flow) Start() {
	f.goRun()
}

// Stop ends all processes and goroutines.
// Idempotent.
// Blocks.
func (f *Flow) Stop() {
	f.quitBarrier.Fall()
	f.quitWait.Wait()
}

func (f *Flow) goRun() {
	f.quitWait.Add(1)
	go func() {
		defer f.quitWait.Done()

		var bufpool *BufPool

		screenName := fmt.Sprintf("rtmptee.%d.source", os.Getpid())
		screens := NewExclusiveScreenService(screenName)
		defer screens.Stop()

		for {

			if bufpool != nil && !bufpool.IsFull() {
				f.log.Warn("Bug: not all buffers were freed; not reusing pool")
				bufpool = nil
			}
			if bufpool == nil {
				bufpool = NewBufPool(f.config.SourceBuffer.BufferCount, f.config.SourceBuffer.BufferSize)
			}

			// Get a screen for the new process
			screen, err := screens.Screen()
			if err != nil {
				f.log.WithError(err).Warn("Failed to start screen")

				// Wait before trying again
				select {
				case <-time.After(f.config.Times.SourceRestartDelay):
					continue
				case <-f.quitBarrier.Barrier():
					return
				}
			}

			// Try to start process
			source := NewSource(f.name, f.sourceCmd, f.config, f.log, bufpool, screen)
			channel := source.Channel()
			if f.config.Times.SourceTimeout > 0 {
				channel = WatchChannel(channel, f.config.Times.SourceTimeout, source.Kill)
			}
			sinks := NewSinkSet(f.sinkCmds, channel, f.config, f.log)
			sinks.Start()

			// Failure?
			if err := source.Start(); err != nil {
				sinks.Stop()
				screens.Done()

				// Wait before trying again
				select {
				case <-time.After(f.config.Times.SourceRestartDelay):
					continue
				case <-f.quitBarrier.Barrier():
					return
				}
			}

			// Wait till it dies (or should die or wants to die)
			select {
			case <-source.DeathBarrier():
			case <-f.quitBarrier.Barrier():
			}

			// Wait till its really dead
			source.Stop()

			sinks.Stop()

			screens.Done()

			// Wait before respawning
			select {
			case <-time.After(f.config.Times.SourceRestartDelay):
				continue
			case <-f.quitBarrier.Barrier():
				return
			}
		}
	}()
}
