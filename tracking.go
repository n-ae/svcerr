package svcerr

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
)

// trackingResponseWriter records whether a response has already been
// committed (a header or body written), so RecoveryMiddleware can tell
// whether it's still safe to write an error response after a panic. It
// implements only http.ResponseWriter itself - Flush and Hijack are added
// by the separate wrapper types below (flushTracker, hijackTracker,
// flushHijackTracker), chosen by newTrackingResponseWriter to match
// exactly what the underlying writer supports. A single type that always
// implemented both regardless of what's underneath would misrepresent the
// underlying writer's real capabilities to a handler's own
// w.(http.Flusher)/w.(http.Hijacker) assertions, and to
// http.ResponseController, which would see a false "supported" instead of
// discovering the truth by unwrapping.
type trackingResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
	status      int
	// hijacked records that commitment came from a successful Hijack
	// rather than a written status - status is then meaningless (no HTTP
	// response was or will be written by this package), and
	// RecoveryMiddleware's committed-panic log says "hijacked" instead of
	// reporting a status of 0 that looks like data.
	hijacked bool
}

func (w *trackingResponseWriter) WriteHeader(status int) {
	// Validate the status here, before recording or delegating anything -
	// the same 100-999 range net/http's own checkWriteHeaderCode accepts.
	// Panicking pre-commitment keeps an invalid status recoverable:
	// nothing reached the connection, so RecoveryMiddleware's recovery can
	// still take its "uncommitted" branch and write a real, valid error
	// response. Before this validation existed, the same outcome depended
	// on delegating first and letting the underlying writer's own
	// validation panic - which forced commitment to be recorded after the
	// delegate call returned, leaving the opposite gap described below.
	if status < 100 || status > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", status))
	}
	// Informational (1xx) responses aren't the final response - net/http
	// allows any number of them before the one commit-worthy final
	// status ("unlike other response headers, informational headers may
	// be written multiple times"), so they must not mark the tracked
	// response committed; a handler that sends one and then panics still
	// needs RecoveryMiddleware to write the real error response. 101
	// Switching Protocols is the exception: it's a protocol transition,
	// not an informational preamble, and no further HTTP response
	// follows on the connection.
	if status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.wroteHeader {
		return
	}
	// Record commitment before delegating, conservatively assuming a
	// valid delegated call may commit before panicking: an intermediate
	// writer sitting between RecoveryMiddleware and the transport whose
	// WriteHeader delegates downstream and then panics (a buggy metrics
	// wrapper, say) never returns here, so recording afterward would
	// leave the response looking uncommitted and let recovery write a
	// second error document onto a status and headers already sent. The
	// cost is the opposite, rarer case: a delegate that panics on a valid
	// status without committing anything is now treated as committed, so
	// recovery aborts the connection (http.ErrAbortHandler) instead of
	// writing a clean 500. Aborting a connection the client can retry is
	// strictly safer than corrupting a response that may already be on
	// the wire, so the conservative direction wins. Statuses a real
	// writer rejects don't pay this cost - they panic in the validation
	// above, before commitment is recorded.
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *trackingResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap exposes the wrapped ResponseWriter to http.ResponseController,
// which looks for this method (or the original ResponseWriter itself) to
// reach capabilities like SetReadDeadline/SetWriteDeadline through a
// wrapper such as this one.
func (w *trackingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// commitOnFlush marks tw committed (implicitly as 200 OK, the same as
// Write) before delegating to f.Flush - shared by flushTracker and
// flushHijackTracker, the only two variants that ever call it (both are
// only constructed when the underlying writer actually supports
// http.Flusher). A successful flush commits the response the same way
// Write does, so it's recorded the same way - otherwise RecoveryMiddleware
// could still believe the response is uncommitted after a flush and write
// a second, corrupting body on top of it if the handler subsequently
// panics.
func commitOnFlush(tw *trackingResponseWriter, f http.Flusher) {
	if !tw.wroteHeader {
		tw.wroteHeader = true
		tw.status = http.StatusOK
	}
	f.Flush()
}

// flushErrorer is the optional method http.ResponseController prefers
// over plain http.Flusher, for an underlying writer that can report a
// flush failure - http.Flusher's Flush() has no return value and so no
// way to signal one. Documented by http.NewResponseController; not a
// named type in net/http.
type flushErrorer interface {
	FlushError() error
}

// commitOnFlushError marks tw committed before delegating to
// fe.FlushError(), matching what a real FlushError implementation - the
// stdlib HTTP/1.1 server's response.FlushError, and anything wrapping it -
// actually does: WriteHeader(200) is sent unconditionally before the
// flush is even attempted, so a flush failure (a broken connection
// partway through) can happen after the status line and headers are
// already on the wire. Marking committed only on success, as if the
// commitment itself were conditional on the flush succeeding, would leave
// RecoveryMiddleware believing it's still safe to write a fresh JSON
// error body - a second response onto a connection that may have already
// received the first one's headers.
func commitOnFlushError(tw *trackingResponseWriter, fe flushErrorer) error {
	if !tw.wroteHeader {
		tw.wroteHeader = true
		tw.status = http.StatusOK
	}
	return fe.FlushError()
}

// commitOnHijack hijacks through hj, marking tw committed on success -
// shared by every *hijackTracker variant, which are only constructed
// when the underlying writer actually supports http.Hijacker. A
// successful hijack hands the raw connection to the caller, so it's
// treated as committing the response - RecoveryMiddleware must never
// attempt to write a JSON error body onto a hijacked connection. The
// connection itself is deliberately not retained: after Hijack the
// caller owns it (net/http's documented contract), and handlers
// legitimately hand it to another goroutine before an unrelated panic,
// so closing it from panic recovery would be a use-after-transfer
// hazard. Recovery only records the fact of the hijack for its log.
func commitOnHijack(tw *trackingResponseWriter, hj http.Hijacker) (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := hj.Hijack()
	if err == nil {
		tw.wroteHeader = true
		tw.hijacked = true
	}
	return conn, rw, err
}

// flushTracker adds http.Flusher to trackingResponseWriter, for an
// underlying writer that supports plain flushing (but not FlushError or
// hijacking).
type flushTracker struct {
	*trackingResponseWriter
	flusher http.Flusher
}

func (w *flushTracker) Flush() { commitOnFlush(w.trackingResponseWriter, w.flusher) }

// flushErrorTracker adds http.Flusher and FlushError() error to
// trackingResponseWriter, for an underlying writer that reports flush
// failures (but doesn't support hijacking). Flush() discards the error -
// http.Flusher's signature has no way to report one - but still delegates
// through FlushError so a real failure isn't treated as a successful
// commit; FlushError() itself is what http.ResponseController actually
// calls (it checks for this method before plain Flusher), which is the
// entire reason this variant exists separately from flushTracker.
type flushErrorTracker struct {
	*trackingResponseWriter
	flushErrorer flushErrorer
}

func (w *flushErrorTracker) Flush() {
	_ = commitOnFlushError(w.trackingResponseWriter, w.flushErrorer)
}

func (w *flushErrorTracker) FlushError() error {
	return commitOnFlushError(w.trackingResponseWriter, w.flushErrorer)
}

// hijackTracker adds http.Hijacker to trackingResponseWriter, for an
// underlying writer that supports hijacking but not flushing.
type hijackTracker struct {
	*trackingResponseWriter
	hijacker http.Hijacker
}

func (w *hijackTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return commitOnHijack(w.trackingResponseWriter, w.hijacker)
}

// flushHijackTracker adds both http.Flusher and http.Hijacker to
// trackingResponseWriter, for an underlying writer that supports both -
// the common case for the stdlib HTTP/1.1 server's own ResponseWriter.
type flushHijackTracker struct {
	*trackingResponseWriter
	flusher  http.Flusher
	hijacker http.Hijacker
}

func (w *flushHijackTracker) Flush() { commitOnFlush(w.trackingResponseWriter, w.flusher) }

func (w *flushHijackTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return commitOnHijack(w.trackingResponseWriter, w.hijacker)
}

// flushErrorHijackTracker adds http.Flusher, FlushError() error, and
// http.Hijacker to trackingResponseWriter, for an underlying writer that
// reports flush failures and supports hijacking.
type flushErrorHijackTracker struct {
	*trackingResponseWriter
	flushErrorer flushErrorer
	hijacker     http.Hijacker
}

func (w *flushErrorHijackTracker) Flush() {
	_ = commitOnFlushError(w.trackingResponseWriter, w.flushErrorer)
}

func (w *flushErrorHijackTracker) FlushError() error {
	return commitOnFlushError(w.trackingResponseWriter, w.flushErrorer)
}

func (w *flushErrorHijackTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return commitOnHijack(w.trackingResponseWriter, w.hijacker)
}

// responseWriterUnwrapper is the interface http.ResponseController looks
// for to continue past a wrapper that doesn't itself implement the
// capability being searched for - documented by http.NewResponseController,
// not a named type in net/http. discoverHijacker/discoverFlusher below walk
// it the same way ResponseController does, so that capability discovery
// here can't be bypassed by unwrapping past this package's own tracker the
// way a direct type assertion could be (see discoverFlusher's doc comment).
type responseWriterUnwrapper interface {
	Unwrap() http.ResponseWriter
}

// discoverHijacker walks w's Unwrap chain - starting with w itself - the
// same way http.ResponseController's Hijack() does: check the current
// layer for http.Hijacker, and if it's not there, continue to
// Unwrap()'s result and try again. Returns nil if no layer implements it.
func discoverHijacker(w http.ResponseWriter) http.Hijacker {
	for {
		if hj, ok := w.(http.Hijacker); ok {
			return hj
		}
		u, ok := w.(responseWriterUnwrapper)
		if !ok {
			return nil
		}
		w = u.Unwrap()
	}
}

// discoverFlusher walks w's Unwrap chain the same way
// http.ResponseController's Flush() does: at each layer, prefer
// FlushError() error if present, else plain Flush() if present, else
// continue to the next layer via Unwrap(). Crucially this checks both
// interfaces at each layer before moving to the next, matching
// ResponseController's actual per-layer priority - a naive "search every
// layer for FlushError, then search every layer for Flusher" would give a
// deeper FlushError priority over a shallower plain Flusher that
// ResponseController would have stopped at first. Returns whichever it
// finds first (at most one of the two return values is non-nil).
func discoverFlusher(w http.ResponseWriter) (flushErrorer, http.Flusher) {
	for {
		if fe, ok := w.(flushErrorer); ok {
			return fe, nil
		}
		if f, ok := w.(http.Flusher); ok {
			return nil, f
		}
		u, ok := w.(responseWriterUnwrapper)
		if !ok {
			return nil, nil
		}
		w = u.Unwrap()
	}
}

// newTrackingResponseWriter wraps w for RecoveryMiddleware's commit
// tracking. It returns the http.ResponseWriter to pass to the handler -
// implementing http.Hijacker if and only if discoverHijacker finds one
// anywhere in w's Unwrap chain, and http.Flusher if and only if
// discoverFlusher finds a flush capability anywhere in that same chain:
// plain Flush(), FlushError() error, or both (FlushError is checked ahead
// of plain Flusher at each layer, matching http.ResponseController's own
// priority - see discoverFlusher). Searching the whole chain, not just w
// itself, matters because trackingResponseWriter.Unwrap() below
// unconditionally exposes the immediate w to http.ResponseController (for
// deadline-related operations) - if capability discovery here only checked
// w directly, a handler calling http.NewResponseController(wrapped).Flush()
// could unwrap straight through this tracker to a real flusher one or more
// layers down (behind an intermediate wrapper that implements only
// Unwrap()), flushing - and thereby committing - the response without this
// package ever finding out. Discovering the same capability
// ResponseController would discover, and exposing it on the returned
// wrapper itself, closes that gap: ResponseController checks the outermost
// writer's own methods before ever calling Unwrap(), so it now matches this
// wrapper immediately instead of reaching through it.
//
// One deliberate asymmetry follows from discoverFlusher: a writer whose
// only flush capability is FlushError() gains a plain Flush() method it
// didn't have, because the flush capability genuinely exists underneath and
// http.Flusher is how handlers conventionally probe for it - an adapter
// over a real capability, not a fabricated one (the FlushError method
// itself is also preserved, so no error information is lost). Nothing else
// is preserved: http.Pusher and io.ReaderFrom in particular are dropped by
// the wrapper, at any depth. The second return value is the
// *trackingResponseWriter base, for reading wroteHeader/status afterward
// regardless of which variant was returned (every variant embeds it by
// pointer, so its state is shared either way).
func newTrackingResponseWriter(w http.ResponseWriter) (http.ResponseWriter, *trackingResponseWriter) {
	base := &trackingResponseWriter{ResponseWriter: w}
	hijacker := discoverHijacker(w)
	flushErr, flusher := discoverFlusher(w)

	if flushErr != nil {
		if hijacker != nil {
			return &flushErrorHijackTracker{trackingResponseWriter: base, flushErrorer: flushErr, hijacker: hijacker}, base
		}
		return &flushErrorTracker{trackingResponseWriter: base, flushErrorer: flushErr}, base
	}

	switch {
	case flusher != nil && hijacker != nil:
		return &flushHijackTracker{trackingResponseWriter: base, flusher: flusher, hijacker: hijacker}, base
	case flusher != nil:
		return &flushTracker{trackingResponseWriter: base, flusher: flusher}, base
	case hijacker != nil:
		return &hijackTracker{trackingResponseWriter: base, hijacker: hijacker}, base
	default:
		return base, base
	}
}
