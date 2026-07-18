package svcerr

import "net/http"

// Middleware for error recovery and logging, using the package-level
// configuration (global status registry, challenge default, and recovery
// header policy, read at response time). A Renderer's Middleware method
// provides the same behavior under that renderer's own immutable
// configuration and logger.
func RecoveryMiddleware(logger Logger) func(http.Handler) http.Handler {
	return recoveryMiddleware(logger, defaultRecoverySettings)
}

// recoveryMiddleware is the shared implementation behind
// RecoveryMiddleware and Renderer.Middleware. settings is called at
// response-write time, so the package-level path observes startup-time
// global reconfiguration while a Renderer supplies its fixed snapshot.
func recoveryMiddleware(logger Logger, settings func() renderSettings) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped, tw := newTrackingResponseWriter(w)
			returnedNormally := false

			defer func() {
				rec := recover()

				// recover() also reports nil when next.ServeHTTP panicked
				// with a literal nil: whether that's true depends on the
				// panicnil GODEBUG default, which Go selects from the
				// *main* module's go directive, not this package's - so a
				// consumer whose own go.mod predates Go 1.21 gets the old
				// behavior regardless of what this module declares. It's
				// also what a bare recover() sees after next.ServeHTTP
				// calls runtime.Goexit. Neither can be told apart from the
				// other through recover() alone, but both are abnormal
				// exits, so returnedNormally - set only after ServeHTTP
				// actually returns - is what distinguishes them from a
				// genuinely uneventful request.
				if rec == nil && returnedNormally {
					return
				}

				if rec == http.ErrAbortHandler {
					// Conventionally used (including by net/http itself,
					// e.g. on client disconnect mid-response) to abort a
					// request without normal error handling. Let it
					// continue up the stack rather than logging it and
					// writing a response.
					panic(rec)
				}

				var err error
				switch v := rec.(type) {
				case error:
					err = WrapInternalError(v, "http_handler", "panic recovered")
				case string:
					err = NewInternalError("http_handler", "panic recovered")
				default:
					err = NewInternalError("http_handler", "unknown panic")
				}

				if tw.wroteHeader {
					// The handler already committed a response before
					// panicking - the status can't be changed at this
					// point, and writing another body would just corrupt
					// what was already sent. Log, then abort the
					// connection instead of returning normally: a plain
					// return here lets net/http treat whatever partial
					// bytes the handler managed to write as a complete,
					// successful response - the client sees a clean 200
					// with a truncated or invalid body and no transport-
					// level signal anything went wrong. http.ErrAbortHandler
					// is net/http's documented signal for exactly this -
					// it closes the HTTP/1 connection (or resets the
					// HTTP/2 stream) without an additional "http: panic
					// serving" log line, unlike a bare re-panic of rec.
					//
					// Fields are built from err's own severity
					// (http.StatusInternalServerError - err is always this
					// package's own WrapInternalError/NewInternalError,
					// always 5xx-classified), not from tw.status: no error
					// response was rendered here, committed or hijacked, so
					// the "status" errorLogFields would otherwise report is
					// fictitious either way - and gating stack_trace on it
					// as if it were a rendered-response status previously
					// meant a panic after a committed non-5xx response (a
					// plain 200, say) or a hijack (status 0) silently lost
					// the one field this log record exists to carry.
					// http_status is deleted for the same reason: it would
					// report the fabricated 500 as if a response carrying
					// it existed. response_committed_status (or hijacked)
					// is the actual, transport-level truth in its place.
					_, fields := errorLogFields(err, http.StatusInternalServerError)
					delete(fields, "http_status")
					fields["panic"] = rec
					fields["method"] = r.Method
					fields["path"] = r.URL.Path
					msg := "Panic recovered in HTTP handler after response was already committed"
					if tw.hijacked {
						// Commitment came from a successful Hijack, not a
						// written status - tw.status is 0, and reporting a
						// zero as response_committed_status would look like
						// data during an incident. Say what happened
						// instead: no HTTP status applies to a hijacked
						// response. The hijacked connection stays untouched:
						// the handler owns it (see commitOnHijack).
						fields["hijacked"] = true
						msg = "Panic recovered in HTTP handler after connection was hijacked"
					} else {
						fields["response_committed_status"] = tw.status
					}
					safeLog(logger, LevelError, err, fields, msg)
					panic(http.ErrAbortHandler)
				}

				// err here is always this package's own WrapInternalError/
				// NewInternalError, whose Details are always nil - it
				// can't produce a marshal failure the way a caller's own
				// SetPublicDetail could, so unlike WriteHTTPError there's
				// no render error worth plumbing through here.
				statusCode, bytesWritten, _, writeErr := writeJSONErrorBody(tw, err, settings())

				_, fields := errorLogFields(err, statusCode)
				fields["panic"] = rec
				fields["method"] = r.Method
				fields["path"] = r.URL.Path
				if writeErr != nil {
					fields["response_write_error"] = writeErr.Error()
					fields["response_bytes_written"] = bytesWritten
				}
				safeLog(logger, LevelError, err, fields, "Panic recovered in HTTP handler")

				if writeErr != nil {
					// The replacement error body itself failed to fully
					// write (client disconnect, expired deadline, ...) -
					// the client may have received a partial, invalid
					// document that looks like a truncated success, the
					// same problem as the already-committed case above.
					// Abort rather than return normally.
					panic(http.ErrAbortHandler)
				}
			}()

			next.ServeHTTP(wrapped, r)
			returnedNormally = true
		})
	}
}
