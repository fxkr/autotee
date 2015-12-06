package rtmptee

import (
	"encoding/json"
	"os"

	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/juju/errors"
)

// Overridden by build.sh
var Version = "devel"

func Main() {
	parser := cli.NewApp()
	parser.Name = "rtmptee"
	parser.Usage = "yada yada"
	parser.ArgsUsage = "config.yml"
	parser.HideHelp = true
	parser.Version = Version

	parser.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "Enable debug message logging",
		},
		cli.BoolFlag{
			Name:  "show-streams",
			Usage: "Show available and active streams",
		},
		cli.BoolFlag{
			Name:  "show-config",
			Usage: "Show current configuration",
		},
	}

	parser.Action = func(c *cli.Context) {

		if c.Bool("debug") {
			log.SetLevel(log.DebugLevel)
		}
		log.SetFormatter(&log.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "15:04:05",
		})

		err := func() error {

			if len(c.Args()) != 1 {
				cli.ShowAppHelp(c)
				os.Exit(1)
			}

			configPath := c.Args().First()
			if configPath == "" {
				cli.ShowAppHelp(c)
				os.Exit(1)
			}

			config, err := LoadConfig(configPath)
			if err != nil {
				return errors.Trace(err)
			}

			if c.Bool("show-config") {
				return ShowConfigMain(config)
			} else if c.Bool("show-streams") {
				return ShowStreamsMain(config)
			} else {
				app := NewApp(config)
				return errors.Trace(app.Run())
			}

		}()

		if err != nil {
			if log.GetLevel() >= log.DebugLevel {
				log.Fatal(errors.Details(err))
			} else {
				log.Fatal(err)
			}
		}
	}

	err := parser.Run(os.Args)
	if err != nil {
		if log.GetLevel() >= log.DebugLevel {
			log.Fatal(errors.Details(err))
		} else {
			log.Fatal(err)
		}
	}
}

func ShowStreamsMain(config *Config) error {
	client := NewNginxRtmp(config)
	streams, err := client.GetActiveStreams()
	if err != nil {
		return err
	}

	result := struct {
		Url              string
		MatchedStreams   []string
		UnmatchedStreams []string
	}{
		config.Server.Url,
		make([]string, 0),
		make([]string, 0),
	}

	for stream := range streams.Iter() {
		matched := false
		for _, flowConfig := range config.Flows {
			if flowConfig.Regexp.MatchString(stream.(string)) {
				matched = true
				break
			}
		}
		if matched {
			result.MatchedStreams = append(result.MatchedStreams, stream.(string))
		} else {
			result.UnmatchedStreams = append(result.UnmatchedStreams, stream.(string))
		}
	}

	bytes, err := json.MarshalIndent(&result, "", "  ")
	if err != nil {
		return errors.Trace(err)
	}

	println(string(bytes))
	return nil
}

func ShowConfigMain(config *Config) error {

	bytes, err := config.Dump()
	if err != nil {
		return errors.Trace(err)
	}

	println(string(bytes))
	return nil
}
