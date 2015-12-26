package rtmptee

import (
	"fmt"
	"io/ioutil"
	"regexp"
	"strings"
	"time"

	"github.com/juju/errors"
	"gopkg.in/yaml.v2"
	"launchpad.net/xmlpath"
)

type Config struct {
	Debug        bool
	Server       ServerConfig
	Metrics      MetricsConfig
	SourceBuffer BufferPoolConfig
	SinkBuffer   BufferConfig
	Flows        map[string]FlowConfig
	Times        TimeConfig
	Misc         MiscConfig
}

type ServerConfig struct {
	Url   string
	App   string
	XPath *xmlpath.Path
}

type MetricsConfig struct {
	Influx *InfluxConfig `yaml:"influx"`
}

type InfluxConfig struct {
	Host     string `yaml:"host"`
	Database string `yaml:"database"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type BufferConfig struct {
	BufferCount int `yaml:"buffer_count"`
}

type BufferPoolConfig struct {
	BufferCount int `yaml:"buffer_count"`
	BufferSize  int `yaml:"buffer_size"`
}

type FlowConfig struct {
	Regexp *regexp.Regexp
	Source CmdData
	Sinks  map[string]CmdData
}

type TimeConfig struct {
	SourceRestartDelay      time.Duration
	SourceTimeout           time.Duration
	SinkRestartDelay        time.Duration
	NginxRtmpPollInterval   time.Duration
	NginxRtmpRequestTimeout time.Duration
	NginxRtmpServerTimeout  time.Duration
	IdleTime                time.Duration
}

type MiscConfig struct {
	ReuseScreens bool
}

var UseDefaults = func(interface{}) error { return nil }

func (tc *Config) UnmarshalYAML(unmarshal func(interface{}) error) error {
	aux := struct {
		Debug        bool                  `yaml:"debug"`
		Server       ServerConfig          `yaml:"server"`
		Metrics      MetricsConfig         `yaml:"metrics"`
		SourceBuffer BufferPoolConfig      `yaml:"source_buffer"`
		SinkBuffer   BufferConfig          `yaml:"sink_buffer"`
		Flows        map[string]FlowConfig `yaml:"flows"`
		Times        TimeConfig            `yaml:"times"`
		Misc         MiscConfig            `yaml:"misc"`
	}{}

	if err := aux.Times.UnmarshalYAML(UseDefaults); err != nil {
		return err
	}
	if err := aux.Misc.UnmarshalYAML(UseDefaults); err != nil {
		return err
	}
	if err := unmarshal(&aux); err != nil {
		return err
	}

	tc.Debug = aux.Debug
	tc.Server = aux.Server
	tc.Metrics = aux.Metrics
	tc.SourceBuffer = aux.SourceBuffer
	tc.SinkBuffer = aux.SinkBuffer
	tc.Flows = aux.Flows
	tc.Times = aux.Times
	tc.Misc = aux.Misc

	return nil
}

func (mc *MiscConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	aux := struct {
		ReuseScreens bool `yaml:"reuse_screens"`
	}{
		ReuseScreens: true,
	}

	if err := unmarshal(&aux); err != nil {
		return errors.Trace(err)
	}

	mc.ReuseScreens = aux.ReuseScreens
	return nil
}

func (tc *TimeConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	aux := struct {
		SourceRestartDelay      int `yaml:"source_restart_delay"`
		SourceTimeout           int `yaml:"source_timeout"`
		SinkRestartDelay        int `yaml:"sink_restart_delay"`
		NginxRtmpPollInterval   int `yaml:"nginx_rtmp_poll_interval"`
		NginxRtmpRequestTimeout int `yaml:"nginx_rtmp_request_timeout"`
		NginxRtmpServerTimeout  int `yaml:"nginx_rtmp_server_timoeut"`
		IdleTime                int `yaml:"idle_time"`
	}{
		SourceRestartDelay:      3,
		SourceTimeout:           3,
		SinkRestartDelay:        3,
		NginxRtmpPollInterval:   5,
		NginxRtmpRequestTimeout: 3,
		NginxRtmpServerTimeout:  16,
		IdleTime:                0,
	}

	if err := unmarshal(&aux); err != nil {
		return errors.Trace(err)
	}

	tc.SourceRestartDelay = time.Duration(aux.SourceRestartDelay) * time.Second
	tc.SourceTimeout = time.Duration(aux.SourceTimeout) * time.Second
	tc.SinkRestartDelay = time.Duration(aux.SinkRestartDelay) * time.Second
	tc.NginxRtmpPollInterval = time.Duration(aux.NginxRtmpPollInterval) * time.Second
	tc.NginxRtmpRequestTimeout = time.Duration(aux.NginxRtmpRequestTimeout) * time.Second
	tc.NginxRtmpServerTimeout = time.Duration(aux.NginxRtmpServerTimeout) * time.Second
	tc.IdleTime = time.Duration(aux.IdleTime) * time.Second
	return nil
}

func (sc *ServerConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	aux := struct {
		Url   string `yaml:"url"`
		App   string `yaml:"app"`
		XPath string `yaml:"xpath"`
	}{
		Url:   "",
		App:   "",
		XPath: defaultXpathStrTemplate,
	}

	if err := unmarshal(&aux); err != nil {
		return errors.Trace(err)
	}
	if aux.Url == "" {
		return errors.New("url setting is required")
	}
	if aux.App == "" {
		return errors.New("app setting is required")
	}
	if aux.XPath == "" {
		return errors.New("xpath setting is required")
	}

	// Fill in nginx-rtmp application name in xpath expression.
	// Proper quoting for XPath is hard, but we don't need it.
	if strings.ContainsAny(aux.App, "'\"") {
		return errors.New("app setting must not contain quotes")
	}
	xpathStr := fmt.Sprintf(aux.XPath, aux.App)

	// Compile XPath expression
	xpath, err := xmlpath.Compile(xpathStr)
	if err != nil {
		return errors.Trace(err)
	}
	sc.XPath = xpath

	sc.Url = aux.Url
	sc.App = aux.App
	return nil
}

func (fc *FlowConfig) UnmarshalYAML(unmarshal func(interface{}) error) (err error) {
	var aux struct {
		Regexp string            `yaml:"regexp"`
		Source string            `yaml:"source"`
		Sinks  map[string]string `yaml:"sinks"`
	}

	if err := unmarshal(&aux); err != nil {
		return errors.Trace(err)
	}

	if fc.Regexp, err = regexp.Compile(aux.Regexp); err != nil {
		return errors.Annotatef(err, "failed to parse regexp in flow config: %#v", aux.Regexp)
	}

	if fc.Source, err = NewCmdData(aux.Source); err != nil {
		return errors.Annotatef(err, "failed to parse source in flow config: %#v", aux.Source)
	}

	fc.Sinks = make(map[string]CmdData, len(aux.Sinks))
	for name, command := range aux.Sinks {
		if fc.Sinks[name], err = NewCmdData(command); err != nil {
			return errors.Annotatef(err, "failed to parse command for sink %s: %s", name, command)
		}
	}

	return nil
}

func LoadConfig(path string) (*Config, error) {
	var config Config

	bytes, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, errors.Annotatef(err, "failed to read config file: %s", path)
	}

	if err = yaml.Unmarshal(bytes, &config); err != nil {
		return nil, errors.Annotatef(err, "failed to parse config file: %s", path)
	}

	return &config, nil
}

func (c *Config) Dump() ([]byte, error) {
	return yaml.Marshal(c)
}
