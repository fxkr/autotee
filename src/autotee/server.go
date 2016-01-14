package autotee

import (
	"github.com/deckarep/golang-set"
)

type Server interface {
	GetActiveStreams() (mapset.Set, error)
}

type ServerFactory func(config *Config) Server
