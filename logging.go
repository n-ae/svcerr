package svcerr

// errorLogFields builds the level and structured fields describing err at
// the given status code. Shared by logError and RecoveryMiddleware, so a
// recovered panic produces one log record carrying both the panic context
// and the usual error fields, rather than each logging independently.
func errorLogFields(err error, statusCode int) (Level, map[string]any) {
	// Determine log level based on status code
	level := LevelInfo
	switch {
	case statusCode >= 500:
		level = LevelError
	case statusCode >= 400:
		level = LevelWarn
	}

	code := GetErrorCode(err)
	fields := map[string]any{
		"error_code":  string(code),
		"http_status": statusCode,
	}

	// Add stack trace for server errors
	if statusCode >= 500 {
		if stack := GetStackTrace(err); len(stack) > 0 {
			fields["stack_trace"] = stack
		}
	}

	// Add type-specific context from the same outermost coded node code
	// itself came from (via GetErrorCode/outermostCoded above) - not an
	// independent errors.As search across the whole chain, which could
	// find and attribute fields from a different (inner) node than the one
	// that produced code/statusCode. extractErrorDetails already follows
	// this rule for the client-facing response body; this mirrors it for
	// logging, for the same reason its own doc comment gives: an outer
	// wrapper's code (e.g. ErrCodeNotFound from WrapNotFoundError) must
	// not end up paired with a wrapped error's details (e.g. a
	// DatabaseError's operation).
	switch v := outermostCoded(err).(type) {
	case *ValidationError:
		fields["field"] = v.field
	case *DatabaseError:
		fields["db_operation"] = v.operation
	case *ExternalAPIError:
		fields["service"] = v.service
		fields["service_status"] = v.statusCode
	case *AuthenticationError:
		fields["auth_reason"] = v.reason
	case *NotFoundError:
		fields["resource_type"] = v.resourceType
		fields["resource_id"] = v.resourceID
	case *ConflictError:
		fields["resource_type"] = v.resourceType
		fields["conflict_key"] = v.conflictKey
	case *RateLimitError:
		fields["service"] = v.service
		fields["limit"] = v.limit
		fields["retry_after"] = v.retryAfter
	case *InternalError:
		fields["component"] = v.component
	}

	return level, fields
}

// logError logs error with appropriate level and context. renderErr is
// the marshal error from writeJSONErrorBody/writeProblemJSONBody when the
// real response body couldn't be encoded and a generic fallback was
// substituted (nil otherwise) - logged as its own field, together with
// the code the client actually received, so the log doesn't just show
// err's original classification with no indication the client got a
// different one. writeErr is whatever the final w.Write returned (nil on
// a full write) - unlike renderErr it doesn't imply a different body was
// sent, only that delivery of the intended one may have failed or been
// truncated (client disconnect, expired deadline, ...), which is
// otherwise invisible: WriteHeader/Write don't return to their caller in
// a way any of this package's writers surface today.
func logError(logger Logger, err error, statusCode int, renderErr, writeErr error, bytesWritten int) {
	level, fields := errorLogFields(err, statusCode)
	if renderErr != nil {
		fields["response_render_error"] = renderErr.Error()
		fields["rendered_error_code"] = string(ErrCodeInternal)
	}
	if writeErr != nil {
		fields["response_write_error"] = writeErr.Error()
		// Only meaningful alongside the failure: how much of the body got
		// out before delivery broke. On a full write it's implied by the
		// body itself, so it isn't logged as noise on every response.
		fields["response_bytes_written"] = bytesWritten
	}
	safeLog(logger, level, err, fields, "HTTP error response")
}

// safeLog calls logger.Log if logger is non-nil, containing a panic from
// within that call so it can't escape and replace whatever this package
// was itself in the middle of reporting. That matters most in
// RecoveryMiddleware: its own recover() has already fired once for the
// handler's original panic by the time it calls safeLog, so a second,
// uncaught panic from a broken Logger would propagate out of that
// already-executing deferred function - past this package entirely,
// caught only by net/http's own outer per-connection recovery, which
// drops the original panic's structured log record (error code, stack
// trace, request path) and prints a generic stdlib trace pointing at the
// logger instead. There's nowhere further to report a logger's own
// failure to - the logger is what would have received that report - so
// it's contained and silently dropped rather than escalated.
//
// A nil Logger is tolerated (not an error) everywhere this package logs,
// so WriteHTTPError/WriteHTTPErrorHTML/WriteHTTPProblem/RecoveryMiddleware
// stay usable by a caller that doesn't want logging at all, without
// forcing them to plumb through a no-op implementation just to avoid a
// nil-pointer panic. Callers who want response rendering with no logging
// contract whatsoever can use WriteJSON/WriteHTML/WriteProblem directly
// instead of passing nil here.
func safeLog(logger Logger, level Level, err error, fields map[string]any, msg string) {
	if logger == nil {
		return
	}
	defer func() { _ = recover() }()
	logger.Log(level, err, fields, msg)
}
