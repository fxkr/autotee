package autotee

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/deckarep/golang-set"
	"github.com/rcrowley/go-metrics"
	"github.com/vrischmann/go-metrics-influxdb"
	"golang.org/x/net/context"
)

type App struct {
	ctx    context.Context
	cancel context.CancelFunc

	Config *Config
	Flows  map[string][]*Flow
}

func NewApp(ctx context.Context, config *Config) *App {
	appCtx, cancel := context.WithCancel(ctx)

	return &App{
		ctx:    appCtx,
		cancel: cancel,

		Config: config,
		Flows:  make(map[string][]*Flow),
	}
}

func (app *App) Run() error {
	go app.handleSigint()
	go app.handleSigusr1()

	if app.Config.Times.IdleTime > 0 {
		go ShowIdleness(app.Config.Times.IdleTime)
	}

	if app.Config.Metrics.Influx != nil {
		go influxdb.InfluxDB(
			metrics.DefaultRegistry,
			10*time.Second,
			app.Config.Metrics.Influx.Host,
			app.Config.Metrics.Influx.Database,
			app.Config.Metrics.Influx.Username,
			app.Config.Metrics.Influx.Password,
		)
	}

	prevStreams := mapset.NewSet()
	server := app.Config.Server.NewServer(app.Config)

	ticker := TickNow(app.Config.Times.ServerPollInterval)
	resetTimer := NewRestartableTimer(app.Config.Times.ServerTimeout)

	numStreamsMetric := metrics.GetOrRegister("streams", metrics.NewGauge()).(metrics.Gauge)
	for {
		select {
		case <-resetTimer.C:
			log.Warn("No reply from server, assuming all streams gone")
			resetTimer.Stop()
			for stream := range prevStreams.Iter() {
				app.removeStream(stream.(string))
			}
			prevStreams = mapset.NewSet()

		case <-ticker:
			curStreams, err := server.GetActiveStreams()
			if err != nil {
				Catch(err)
				continue
			}
			resetTimer.Restart()

			numStreamsMetric.Update(int64(curStreams.Cardinality()))

			for stream := range curStreams.Difference(prevStreams).Iter() {
				app.addStream(stream.(string))
			}
			for stream := range prevStreams.Difference(curStreams).Iter() {
				app.removeStream(stream.(string))
			}

			// All streams gone? => Good time for a GC run
			if prevStreams.Cardinality() > 0 && curStreams.Cardinality() == 0 {
				debug.FreeOSMemory()
			}

			prevStreams = curStreams

		case <-app.ctx.Done():
			resetTimer.Stop()
			for _, flows := range app.Flows {
				for _, flow := range flows {
					flow.Stop()
				}
			}
			return nil
		}
	}
}

func (app *App) addStream(stream string) {
	logged := false

	for flowName, flowConfig := range app.Config.Flows {
		if flowConfig.Regexp.MatchString(stream) {
			if !logged {
				logged = true
				log.WithFields(log.Fields{
					"stream": stream,
					"match":  true,
				}).Warn("New stream")
			}

			app.addFlow(flowName, stream, flowConfig.Source, flowConfig.Sinks)
		}
	}

	if !logged {
		log.WithFields(log.Fields{
			"stream": stream,
			"match":  false,
		}).Debug("New stream, ignoring")
	}
}

func (app *App) addFlow(name string, stream string, sourceTemplate CmdData, sinkTemplates map[string]CmdData) {
	vars := map[string]string{
		"{stream}": stream,
	}

	source := sourceTemplate.Replace(vars)
	sinks := make(map[string]CmdData, len(sinkTemplates))
	for sinkName, sinkTemplate := range sinkTemplates {
		sinks[sinkName] = sinkTemplate.Replace(vars)
	}

	flow := NewFlow(app.ctx, name, app.Config, source, sinks, log.WithFields(log.Fields{
		"name":   name,
		"stream": stream,
	}))
	flow.Start()

	if _, ok := app.Flows[stream]; !ok {
		app.Flows[stream] = make([]*Flow, 0, 1)
	}
	app.Flows[stream] = append(app.Flows[stream], flow)
}

func (app *App) removeStream(stream string) {
	flows, ok := app.Flows[stream]
	if !ok {
		log.WithFields(log.Fields{
			"stream": stream,
		}).Debug("Ignored stream gone")

		return
	}

	log.WithFields(log.Fields{
		"stream": stream,
	}).Warn("Stream gone")

	for _, flow := range flows {
		flow.log.Info("Stopping flow")
		flow.Stop()
	}

	delete(app.Flows, stream)
}

func (app *App) handleSigint() {
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, os.Interrupt)
	<-channel
	if app.Config.Debug {
		panic("Interrupted")
	} else {
		log.Info("Interrupted, shutting down...")
		app.cancel()
	}
}

func (app *App) handleSigusr1() {
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, syscall.SIGUSR1)
	for range channel {
		stack := make([]byte, 524288)
		length := runtime.Stack(stack, true)
		name := fmt.Sprintf("/tmp/autotee.%d.stack", os.Getpid())

		f, err := os.Create(name)
		if err != nil {
			continue
		}
		f.Write(stack[:length])
		f.Close()

		log.Infof("Wrote stacktrace to %s", name)
	}
}
