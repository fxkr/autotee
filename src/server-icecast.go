package autotee

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/deckarep/golang-set"
	"github.com/juju/errors"
)

type IcecastUrls struct {
	mapset.Set
}

type Icecast struct {
	config *Config
	client http.Client
}

func NewIcecast(config *Config) Server {
	return &Icecast{
		config: config,
		client: http.Client{Timeout: config.Times.ServerRequestTimeout},
	}
}

// Get a list of active streams of a specific app from an Icecast server.
func (nr *Icecast) GetActiveStreams() (mapset.Set, error) {

	// Make HTTP request
	resp, err := nr.client.Get(nr.config.Server.Url)
	if err != nil {
		return nil, errors.Trace(err)
	}
	defer resp.Body.Close()

	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Trace(err)
	}

	urls := IcecastUrls{mapset.NewSet()}
	err = json.Unmarshal(bytes, &urls)
	if err != nil {
		return nil, errors.Trace(err)
	}

	return urls.Set, nil
}

func (iu *IcecastUrls) UnmarshalJSON(bytes []byte) error {

	var aux struct {
		Icestats struct {
			// No source => missing (nil)
			// One source => dict (map[string]interface{})
			// More sources => list ([]interface{})
			Source interface{} `json:"source"`
		} `json:"icestats"`
	}

	if err := json.Unmarshal(bytes, &aux); err != nil {
		return errors.Trace(err)
	}

	switch obj := aux.Icestats.Source.(type) {

	// No source?
	case nil:
		return nil

	// Exactly one source?
	default:
		return iu.addSourceObj(obj)

	// More than one source?
	case []interface{}:
		for _, subobj := range obj {
			if err := iu.addSourceObj(subobj); err != nil {
				return errors.Trace(err)
			}
		}
		return nil
	}
}

func (iu *IcecastUrls) addSourceObj(sourceObj interface{}) error {
	m, ok := sourceObj.(map[string]interface{})
	if !ok {
		return errors.New("source wasn't a JSON object")
	}

	urlObj, ok := m["listenurl"]
	if !ok {
		return errors.New("listenurl field not present")
	}

	urlStr, ok := urlObj.(string)
	if !ok {
		return errors.New("listenurl field did not contain a string")
	}

	url, err := url.Parse(urlStr)
	if !ok {
		return errors.Annotate(err, "failed to parse listenurl")
	}

	iu.Add(strings.TrimLeft(url.Path, "/"))
	return nil
}
