package errors

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
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

// HTTPStatusCode maps error codes to HTTP status codes
func HTTPStatusCode(code ErrorCode) int {
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

	// Add Retry-After header for rate limit errors
	var rle *RateLimitError
	if errors.As(err, &rle) {
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
		var pm publicMessager
		if errors.As(err, &pm) {
			if msg, ok := pm.PublicMessage(); ok {
				return msg
			}
		}

		// Error() is only safe to surface as-is when the error wasn't
		// built by wrapping another error (Wrap*) - a wrapped cause's
		// text may carry internal detail (raw SQL, connection strings,
		// third-party error text) that must never reach a client without
		// an explicit SetPublicMessage override.
		var ewc ErrorWithCode
		if errors.As(err, &ewc) && ewc.Unwrap() == nil {
			// For validation errors, include field information
			var ve *ValidationError
			if errors.As(err, &ve) && ve.Field != "" {
				return ve.Error()
			}
			return ewc.Error()
		}
	}

	// Default messages by code
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

// extractErrorDetails extracts contextual details from error
func extractErrorDetails(err error) map[string]interface{} {
	details := make(map[string]interface{})

	// Extract details from custom error types
	var ve *ValidationError
	var de *DatabaseError
	var ee *ExternalAPIError
	var ne *NotFoundError
	var rle *RateLimitError

	switch {
	case errors.As(err, &ve):
		if ve.Field != "" {
			details["field"] = ve.Field
		}
		// ve.Value is deliberately not included here - it's whatever the
		// caller passed in (a password, a token, an oversized payload),
		// and this package has no way to know it's safe to publish.
	case errors.As(err, &de):
		if de.Operation != "" {
			details["operation"] = de.Operation
		}
	case errors.As(err, &ee):
		details["service"] = ee.Service
		if ee.StatusCode > 0 {
			details["status_code"] = ee.StatusCode
		}
		if ee.RetryAfter != nil {
			details["retry_after"] = *ee.RetryAfter
		}
	case errors.As(err, &ne):
		details["resource_type"] = ne.ResourceType
		if ne.ResourceID != "" {
			details["resource_id"] = ne.ResourceID
		}
	case errors.As(err, &rle):
		details["limit"] = rle.Limit
		details["retry_after"] = rle.RetryAfter
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
