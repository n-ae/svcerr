// Package zerologadapter adapts a zerolog.Logger to the svcerr.Logger
// interface. It's optional and depends on zerolog - the parent svcerr
// package does not; importing this subpackage is what pulls zerolog into a
// caller's build.
package zerologadapter

import (
	"github.com/n-ae/svcerr"
	"github.com/rs/zerolog"
)

// Adapter wraps a zerolog.Logger to satisfy svcerr.Logger.
type Adapter struct {
	log zerolog.Logger
}

// New wraps l for use as a svcerr.Logger.
func New(l zerolog.Logger) Adapter {
	return Adapter{log: l}
}

// Log implements svcerr.Logger.
func (a Adapter) Log(level svcerr.Level, err error, fields map[string]interface{}, msg string) {
	var event *zerolog.Event
	switch level {
	case svcerr.LevelError:
		event = a.log.Error()
	case svcerr.LevelWarn:
		event = a.log.Warn()
	default:
		event = a.log.Info()
	}

	if err != nil {
		event = event.Err(err)
	}
	for k, v := range fields {
		event = event.Interface(k, v)
	}
	event.Msg(msg)
}
