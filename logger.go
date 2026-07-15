package errors

// Level is a log severity, independent of any specific logging library.
type Level int

const (
	LevelInfo Level = iota
	LevelWarn
	LevelError
)

// Logger is the minimal structured-logging surface this package needs. It has
// no dependency on any specific logging library - wrap your logger to satisfy
// it. See the zerologadapter subpackage for a zerolog adapter.
type Logger interface {
	// Log records msg at the given level. err may be nil (e.g. the panic
	// path logs a message with no associated error). fields carries
	// structured context (error_code, http_status, stack_trace, and
	// error-type-specific keys like "field" or "resource_id").
	Log(level Level, err error, fields map[string]interface{}, msg string)
}
