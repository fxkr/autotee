package autotee

import (
	"github.com/deckarep/golang-set"
)

type StaticStreamList struct {
	config *Config
}

func NewStaticStreamList(config *Config) Server {
	return &StaticStreamList{config}
}

func (nr *StaticStreamList) GetActiveStreams() (mapset.Set, error) {
	return nr.config.Server.Streams, nil
}
