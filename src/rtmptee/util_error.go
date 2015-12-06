package rtmptee

import (
	log "github.com/Sirupsen/logrus"
	"github.com/juju/errors"
)

func Catch(err error) error {
	if log.GetLevel() >= log.DebugLevel {
		log.Warn(errors.Details(err))
	} else {
		log.Warn(err)
	}
	return err
}
