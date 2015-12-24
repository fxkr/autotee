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

type FlowCmdData struct {
	CmdData

	screens ScreenService
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

		sourceScreenName := fmt.Sprintf("rtmptee.%d.source", os.Getpid())
		var sourceScreens ScreenService
		if f.config.Misc.ReuseScreens {
			sourceScreens = NewSharedScreenService(sourceScreenName)
		} else {
			sourceScreens = NewExclusiveScreenService(sourceScreenName)
		}
		defer sourceScreens.Stop()

		sinkCmds := make(map[string]SinkCmdData, len(f.sinkCmds))
		for name, sinkCmd := range f.sinkCmds {

			var sinkScreens ScreenService
			sinkScreenName := fmt.Sprintf("rtmptee.%d.sink", os.Getpid())
			if f.config.Misc.ReuseScreens {
				sinkScreens = NewSharedScreenService(sinkScreenName)
			} else {
				sinkScreens = NewExclusiveScreenService(sinkScreenName)
			}
			defer sinkScreens.Stop()

			sinkCmds[name] = SinkCmdData{
				Screens: sinkScreens,
				Command: sinkCmd,
			}
		}

		for {

			if bufpool != nil && !bufpool.IsFull() {
				f.log.Warn("Bug: not all buffers were freed; not reusing pool")
				bufpool = nil
			}
			if bufpool == nil {
				bufpool = NewBufPool(f.config.SourceBuffer.BufferCount, f.config.SourceBuffer.BufferSize)
			}

			// Get a screen for the new process
			screen, err := sourceScreens.Screen()
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
			sinks := NewSinkSet(sinkCmds, channel, f.config, f.log)
			sinks.Start()

			// Failure?
			if err := source.Start(); err != nil {
				sinks.Stop()
				sourceScreens.Done()

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

			// Stop the screens (may block some time, so do it in parallel)
			var screensStopped sync.WaitGroup
			screensStopped.Add(1)
			go func() {
				sourceScreens.Done()
				screensStopped.Done()
			}()
			for _, sinkCmdData := range sinkCmds {
				screensStopped.Add(1)
				go func(s SinkCmdData) {
					s.Screens.Done()
					screensStopped.Done()
				}(sinkCmdData)
			}
			screensStopped.Wait()

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
