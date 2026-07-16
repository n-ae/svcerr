package svcerr

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"sync"
)

// HTTPErrorResponse represents a standardized HTTP error response
type HTTPErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains detailed error information for API responses
type ErrorDetail struct {
	Code    ErrorCode              `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

var (
	customStatusMu   sync.RWMutex
	customStatusCode = map[ErrorCode]int{}
)

// RegisterStatusCode adds or overrides the HTTP status HTTPStatusCode
// returns for code. Use it to extend the built-in mapping with an
// application's own ErrorCode values (constructed via New/Wrap), or to
// override a built-in mapping for a deployment that wants different
// semantics. Safe for concurrent use.
func RegisterStatusCode(code ErrorCode, status int) {
	customStatusMu.Lock()
	defer customStatusMu.Unlock()
	customStatusCode[code] = status
}

// HTTPStatusCode maps error codes to HTTP status codes
func HTTPStatusCode(code ErrorCode) int {
	customStatusMu.RLock()
	status, ok := customStatusCode[code]
	customStatusMu.RUnlock()
	if ok {
		return status
	}

	switch code {
	// Validation errors -> 400 Bad Request
	case ErrCodeInvalidInput, ErrCodeMissingRequired, ErrCodeInvalidFormat, ErrCodeConstraintViolation:
		return http.StatusBadRequest

	// Authentication errors -> 401 Unauthorized or 403 Forbidden
	case ErrCodeUnauthorized, ErrCodeTokenExpired, ErrCodeTokenInvalid:
		return http.StatusUnauthorized
	case ErrCodePermissionDenied:
		return http.StatusForbidden

	// Resource errors -> 404 Not Found or 409 Conflict
	case ErrCodeNotFound:
		return http.StatusNotFound
	case ErrCodeAlreadyExists, ErrCodeResourceConflict:
		return http.StatusConflict

	// Rate limiting -> 429 Too Many Requests
	case ErrCodeRateLimitExceeded, ErrCodeQuotaExceeded:
		return http.StatusTooManyRequests

	// External API errors -> 502 Bad Gateway
	case ErrCodeExternalAPI:
		return http.StatusBadGateway

	// An unreachable database is a transient condition -> 503 Service
	// Unavailable. A malformed query or a failed transaction/migration is
	// a bug or invariant failure on this end, not something a client
	// retry fixes -> 500, same as any other internal error.
	case ErrCodeDatabaseConnection:
		return http.StatusServiceUnavailable
	case ErrCodeDatabaseQuery, ErrCodeDatabaseTransaction, ErrCodeDatabaseMigration:
		return http.StatusInternalServerError

	// Internal errors -> 500 Internal Server Error
	case ErrCodeInternal:
		return http.StatusInternalServerError

	// Not implemented -> 501 Not Implemented
	case ErrCodeNotImplemented:
		return http.StatusNotImplemented

	default:
		return http.StatusInternalServerError
	}
}

// WriteHTTPError writes a standardized error response to the HTTP response writer
func WriteHTTPError(w http.ResponseWriter, err error, logger Logger) {
	statusCode := writeJSONErrorBody(w, err)
	logError(logger, err, statusCode)
}

// writeJSONErrorBody writes err's JSON body and headers to w and returns the
// status code used, without logging. Split out of WriteHTTPError so
// RecoveryMiddleware can write the response and log the panic as a single
// record instead of the response write and the log call each logging
// independently.
func writeJSONErrorBody(w http.ResponseWriter, err error) int {
	code := GetErrorCode(err)
	statusCode := HTTPStatusCode(code)

	errResp := HTTPErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: getUserFriendlyMessage(code, err),
			Details: extractErrorDetails(err),
		},
	}

	w.Header().Set("Content-Type", "application/json")

	// Add Retry-After header for rate limit errors - keyed off the same
	// outermost-coded node as extractErrorDetails, so an outer wrapper's
	// code can't inherit a wrapped RateLimitError's header.
	if rle, ok := outermostCoded(err).(*RateLimitError); ok {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", rle.RetryAfter))
	}

	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(errResp)

	return statusCode
}

// WriteHTTPErrorHTML writes an HTML error response (for non-API endpoints)
func WriteHTTPErrorHTML(w http.ResponseWriter, err error, logger Logger) {
	statusCode := writeHTMLErrorBody(w, err)
	logError(logger, err, statusCode)
}

// writeHTMLErrorBody mirrors writeJSONErrorBody for the HTML response.
func writeHTMLErrorBody(w http.ResponseWriter, err error) int {
	code := GetErrorCode(err)
	statusCode := HTTPStatusCode(code)
	message := getUserFriendlyMessage(code, err)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)

	body := `<div class="error-message" role="alert">` +
		`<h3>Error</h3>` +
		`<p>` + html.EscapeString(message) + `</p>` +
		`</div>`

	_, _ = w.Write([]byte(body))

	return statusCode
}

// ProblemDetails is the RFC 9457 (https://www.rfc-editor.org/rfc/rfc9457)
// "application/problem+json" response body written by WriteHTTPProblem.
// Code and any extractErrorDetails fields are extension members - RFC 9457
// says extension members live at the top level alongside the registered
// ones, which is what MarshalJSON does instead of nesting them.
type ProblemDetails struct {
	Type       string                 // a URI reference identifying the problem type; "about:blank" when none is registered
	Title      string                 // a short, occurrence-invariant summary of the problem type
	Status     int                    // the HTTP status code for this occurrence
	Detail     string                 // a human-readable explanation specific to this occurrence
	Instance   string                 // a URI reference identifying this specific occurrence, if known
	Code       ErrorCode              // this package's own error code, as an extension member
	Extensions map[string]interface{} // additional extension members (e.g. resource_id, field)
}

// MarshalJSON flattens Extensions into the top-level object rather than
// nesting them under a sub-object, per RFC 9457's extension-member model.
func (p ProblemDetails) MarshalJSON() ([]byte, error) {
	out := make(map[string]interface{}, len(p.Extensions)+5)
	for k, v := range p.Extensions {
		out[k] = v
	}
	out["type"] = p.Type
	out["title"] = p.Title
	out["status"] = p.Status
	if p.Detail != "" {
		out["detail"] = p.Detail
	}
	if p.Instance != "" {
		out["instance"] = p.Instance
	}
	out["code"] = p.Code
	return json.Marshal(out)
}

// WriteHTTPProblem writes an RFC 9457 "application/problem+json" error
// response - an alternative body shape to WriteHTTPError's own
// {"error": {...}} for callers whose clients expect the standard
// problem-details format. Status mapping, message safety (Detail never
// includes a wrapped cause's text without an explicit SetPublicMessage),
// and logging behave identically to WriteHTTPError.
func WriteHTTPProblem(w http.ResponseWriter, err error, logger Logger) {
	statusCode := writeProblemJSONBody(w, err)
	logError(logger, err, statusCode)
}

// writeProblemJSONBody mirrors writeJSONErrorBody for the problem+json body.
func writeProblemJSONBody(w http.ResponseWriter, err error) int {
	code := GetErrorCode(err)
	statusCode := HTTPStatusCode(code)

	problem := ProblemDetails{
		Type:       "about:blank",
		Title:      defaultMessageForCode(code),
		Status:     statusCode,
		Detail:     getUserFriendlyMessage(code, err),
		Code:       code,
		Extensions: extractErrorDetails(err),
	}

	w.Header().Set("Content-Type", "application/problem+json")

	if rle, ok := outermostCoded(err).(*RateLimitError); ok {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", rle.RetryAfter))
	}

	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(problem)

	return statusCode
}

// UserMessage returns the safe, user-facing message for an error - the same
// sanitized text WriteHTTPError/WriteHTTPErrorHTML send (e.g. a wrapped
// database error's raw cause is never included), for callers that need to
// embed it in a custom response fragment instead of one of those two
// standard bodies.
func UserMessage(err error) string {
	return getUserFriendlyMessage(GetErrorCode(err), err)
}

// getUserFriendlyMessage returns a user-friendly error message
func getUserFriendlyMessage(code ErrorCode, err error) string {
	// If it's a known error type, use its message
	if err != nil {
		// An explicit SetPublicMessage override always wins.
		var pm PublicMessager
		if errors.As(err, &pm) {
			if msg, ok := pm.PublicMessage(); ok {
				return msg
			}
		}

		// Error() is only safe to surface as-is when the error wasn't
		// built by wrapping another error (Wrap*) - a wrapped cause's
		// text may carry internal detail (raw SQL, connection strings,
		// third-party error text) that must never reach a client without
		// an explicit SetPublicMessage override. This only needs a coded,
		// unwrappable error - not the full ErrorWithCode (no StackTrace
		// requirement) - so a minimal external error type implementing
		// just Coder, error, and Unwrap still gets this safety property.
		var cu interface {
			error
			Coder
			Unwrap() error
		}
		if errors.As(err, &cu) && cu.Unwrap() == nil {
			// For validation errors, include field information
			var ve *ValidationError
			if errors.As(err, &ve) && ve.Field != "" {
				return ve.Error()
			}
			return cu.Error()
		}
	}

	return defaultMessageForCode(code)
}

// defaultMessageForCode returns the generic, occurrence-invariant
// client-facing message for code - used both as getUserFriendlyMessage's
// fallback and as the RFC 9457 "title" member in WriteHTTPProblem, which
// (per RFC 9457) should describe the general problem type rather than one
// specific occurrence of it.
func defaultMessageForCode(code ErrorCode) string {
	switch code {
	case ErrCodeInvalidInput, ErrCodeInvalidFormat, ErrCodeConstraintViolation:
		return "Invalid input provided. Please check your request and try again."
	case ErrCodeMissingRequired:
		return "Required field is missing."
	case ErrCodeUnauthorized:
		return "Authentication required. Please log in."
	case ErrCodeTokenExpired:
		return "Your session has expired. Please log in again."
	case ErrCodeTokenInvalid:
		return "Invalid authentication token."
	case ErrCodePermissionDenied:
		return "You don't have permission to access this resource."
	case ErrCodeNotFound:
		return "The requested resource was not found."
	case ErrCodeAlreadyExists:
		return "A resource with this identifier already exists."
	case ErrCodeResourceConflict:
		return "The request conflicts with the current state of the resource."
	case ErrCodeRateLimitExceeded:
		return "Too many requests. Please try again later."
	case ErrCodeQuotaExceeded:
		return "You have exceeded your allotted quota."
	case ErrCodeExternalAPI:
		return "External service is temporarily unavailable. Please try again later."
	case ErrCodeDatabaseConnection, ErrCodeDatabaseQuery, ErrCodeDatabaseTransaction, ErrCodeDatabaseMigration:
		return "Database error occurred. Please try again."
	case ErrCodeInternal:
		return "An internal error occurred. Please contact support if the problem persists."
	case ErrCodeNotImplemented:
		return "This functionality is not yet implemented."
	default:
		return "An unexpected error occurred."
	}
}

// extractErrorDetails extracts contextual details from the outermost coded
// error in err's chain - the same node whose code selects the HTTP status
// and message (see outermostCoded) - so a wrapper's code is never paired
// with a wrapped error's details.
func extractErrorDetails(err error) map[string]interface{} {
	details := make(map[string]interface{})

	switch v := outermostCoded(err).(type) {
	case *ValidationError:
		if v.Field != "" {
			details["field"] = v.Field
		}
		// v.Value is deliberately not included here - it's whatever the
		// caller passed in (a password, a token, an oversized payload),
		// and this package has no way to know it's safe to publish.
	case *DatabaseError:
		if v.Operation != "" {
			details["operation"] = v.Operation
		}
	case *ExternalAPIError:
		details["service"] = v.Service
		if v.StatusCode > 0 {
			details["status_code"] = v.StatusCode
		}
		if v.RetryAfter != nil {
			details["retry_after"] = *v.RetryAfter
		}
	case *NotFoundError:
		details["resource_type"] = v.ResourceType
		if v.ResourceID != "" {
			details["resource_id"] = v.ResourceID
		}
	case *RateLimitError:
		details["limit"] = v.Limit
		details["retry_after"] = v.RetryAfter
	}

	if len(details) == 0 {
		return nil
	}
	return details
}

// errorLogFields builds the level and structured fields describing err at
// the given status code. Shared by logError and RecoveryMiddleware, so a
// recovered panic produces one log record carrying both the panic context
// and the usual error fields, rather than each logging independently.
func errorLogFields(err error, statusCode int) (Level, map[string]interface{}) {
	// Determine log level based on status code
	level := LevelInfo
	switch {
	case statusCode >= 500:
		level = LevelError
	case statusCode >= 400:
		level = LevelWarn
	}

	code := GetErrorCode(err)
	fields := map[string]interface{}{
		"error_code":  string(code),
		"http_status": statusCode,
	}

	// Add stack trace for server errors
	if statusCode >= 500 {
		if stack := GetStackTrace(err); len(stack) > 0 {
			fields["stack_trace"] = stack
		}
	}

	// Add type-specific context
	var ve *ValidationError
	var de *DatabaseError
	var ee *ExternalAPIError
	var ae *AuthenticationError
	var ne *NotFoundError

	switch {
	case errors.As(err, &ve):
		fields["field"] = ve.Field
	case errors.As(err, &de):
		fields["db_operation"] = de.Operation
	case errors.As(err, &ee):
		fields["service"] = ee.Service
		fields["service_status"] = ee.StatusCode
	case errors.As(err, &ae):
		fields["auth_reason"] = ae.Reason
	case errors.As(err, &ne):
		fields["resource_type"] = ne.ResourceType
		fields["resource_id"] = ne.ResourceID
	}

	return level, fields
}

// logError logs error with appropriate level and context
func logError(logger Logger, err error, statusCode int) {
	level, fields := errorLogFields(err, statusCode)
	logger.Log(level, err, fields, "HTTP error response")
}

// trackingResponseWriter records whether a response has already been
// committed (a header or body written), so RecoveryMiddleware can tell
// whether it's still safe to write an error response after a panic.
type trackingResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
	status      int
}

func (w *trackingResponseWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *trackingResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Flush implements http.Flusher by delegating to the wrapped
// ResponseWriter when it supports flushing (e.g. for SSE handlers), and is
// a no-op otherwise - http.Flusher's signature has no way to report "not
// supported". A flush commits the response (implicitly as 200 OK if no
// status was written yet) the same way Write does, so it's recorded the
// same way - otherwise RecoveryMiddleware could still believe the response
// is uncommitted after a flush and write a second, corrupting body on top
// of it if the handler subsequently panics.
func (w *trackingResponseWriter) Flush() {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.status = http.StatusOK
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the wrapped ResponseWriter to http.ResponseController,
// which looks for this method (or the original ResponseWriter itself) to
// reach capabilities like SetReadDeadline/SetWriteDeadline through a
// wrapper such as this one.
func (w *trackingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Hijack implements http.Hijacker by delegating to the wrapped
// ResponseWriter when it supports hijacking (e.g. for a WebSocket
// upgrade). A successful hijack hands the raw connection to the caller, so
// it's treated as committing the response - RecoveryMiddleware must never
// attempt to write a JSON error body onto a hijacked connection.
func (w *trackingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("svcerr: underlying http.ResponseWriter does not implement http.Hijacker")
	}
	conn, rw, err := hj.Hijack()
	if err == nil {
		w.wroteHeader = true
	}
	return conn, rw, err
}

// Middleware for error recovery and logging
func RecoveryMiddleware(logger Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tw := &trackingResponseWriter{ResponseWriter: w}

			defer func() {
				rec := recover()
				if rec == nil {
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
					// what was already sent, so only log.
					_, fields := errorLogFields(err, tw.status)
					fields["panic"] = rec
					fields["method"] = r.Method
					fields["path"] = r.URL.Path
					fields["response_committed_status"] = tw.status
					logger.Log(LevelError, err, fields, "Panic recovered in HTTP handler after response was already committed")
					return
				}

				statusCode := writeJSONErrorBody(tw, err)

				_, fields := errorLogFields(err, statusCode)
				fields["panic"] = rec
				fields["method"] = r.Method
				fields["path"] = r.URL.Path
				logger.Log(LevelError, err, fields, "Panic recovered in HTTP handler")
			}()

			next.ServeHTTP(tw, r)
		})
	}
}
