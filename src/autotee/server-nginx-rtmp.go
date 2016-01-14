package autotee

import (
	"net/http"

	"github.com/deckarep/golang-set"
	"github.com/juju/errors"
	"launchpad.net/xmlpath"
)

// XPath expression matching the names of active streams in an
// nginx-rtmp servers stat xml document.
const defaultXpathStrTemplate = "/rtmp/server/application[name/text()='%s']/live/stream[active]/name/text()"

type NginxRtmp struct {
	config *Config
	client http.Client
}

func NewNginxRtmp(config *Config) Server {
	return &NginxRtmp{
		client: http.Client{Timeout: config.Times.ServerRequestTimeout},
		config: config,
	}
}

// Get a list of active streams of a specific app from an nginx-rtmp server.
func (nr *NginxRtmp) GetActiveStreams() (mapset.Set, error) {
	result := mapset.NewSet()

	// Make HTTP request
	resp, err := nr.client.Get(nr.config.Server.Url)
	if err != nil {
		return nil, errors.Trace(err)
	}
	defer resp.Body.Close()

	// Parse XML
	root, err := xmlpath.Parse(resp.Body)
	if err != nil {
		return nil, errors.Trace(err)
	}

	// Extract stream names
	iter := nr.config.Server.XPath.Iter(root)
	for iter.Next() {
		name := iter.Node().String()
		result.Add(name)
	}

	return result, nil
}
