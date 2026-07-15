package errors

import (
	"encoding/json"
	"fmt"
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

	// Database errors -> 503 Service Unavailable
	case ErrCodeDatabaseConnection, ErrCodeDatabaseQuery, ErrCodeDatabaseTransaction, ErrCodeDatabaseMigration:
		return http.StatusServiceUnavailable

	// Internal errors -> 500 Internal Server Error
	case ErrCodeInternal, ErrCodePanic, ErrCodeNotImplemented:
		return http.StatusInternalServerError

	default:
		return http.StatusInternalServerError
	}
}

// WriteHTTPError writes a standardized error response to the HTTP response writer
func WriteHTTPError(w http.ResponseWriter, err error, logger Logger) {
	// Extract error details
	code := GetErrorCode(err)
	statusCode := HTTPStatusCode(code)

	// Build error response
	errResp := HTTPErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: getUserFriendlyMessage(code, err),
			Details: extractErrorDetails(err),
		},
	}

	// Log error with context
	logError(logger, err, statusCode)

	// Set headers
	w.Header().Set("Content-Type", "application/json")

	// Add Retry-After header for rate limit errors
	if rle, ok := err.(*RateLimitError); ok {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", rle.RetryAfter))
	}

	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(errResp)
}

// WriteHTTPErrorHTML writes an HTML error response (for non-API endpoints)
func WriteHTTPErrorHTML(w http.ResponseWriter, err error, logger Logger) {
	code := GetErrorCode(err)
	statusCode := HTTPStatusCode(code)
	message := getUserFriendlyMessage(code, err)

	// Log error
	logError(logger, err, statusCode)

	// Write simple HTML response
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)

	html := `<div class="error-message" role="alert">` +
		`<h3>Error</h3>` +
		`<p>` + message + `</p>` +
		`</div>`

	_, _ = w.Write([]byte(html))
}

// getUserFriendlyMessage returns a user-friendly error message
func getUserFriendlyMessage(code ErrorCode, err error) string {
	// If it's a known error type, use its message
	if err != nil {
		// For validation errors, include field information
		if ve, ok := err.(*ValidationError); ok {
			if ve.Field != "" {
				return ve.Error()
			}
		}

		// For other custom errors, return their message
		if ewc, ok := err.(ErrorWithCode); ok {
			return ewc.Error()
		}
	}

	// Default messages by code
	switch code {
	case ErrCodeInvalidInput:
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
	case ErrCodeRateLimitExceeded:
		return "Too many requests. Please try again later."
	case ErrCodeExternalAPI:
		return "External service is temporarily unavailable. Please try again later."
	case ErrCodeDatabaseConnection, ErrCodeDatabaseQuery:
		return "Database error occurred. Please try again."
	case ErrCodeInternal:
		return "An internal error occurred. Please contact support if the problem persists."
	default:
		return "An unexpected error occurred."
	}
}

// extractErrorDetails extracts contextual details from error
func extractErrorDetails(err error) map[string]interface{} {
	details := make(map[string]interface{})

	// Extract details from custom error types
	switch e := err.(type) {
	case *ValidationError:
		if e.Field != "" {
			details["field"] = e.Field
		}
		if e.Value != nil {
			details["value"] = e.Value
		}
	case *DatabaseError:
		if e.Operation != "" {
			details["operation"] = e.Operation
		}
	case *ExternalAPIError:
		details["service"] = e.Service
		if e.StatusCode > 0 {
			details["status_code"] = e.StatusCode
		}
		if e.RetryAfter != nil {
			details["retry_after"] = *e.RetryAfter
		}
	case *NotFoundError:
		details["resource_type"] = e.ResourceType
		if e.ResourceID != "" {
			details["resource_id"] = e.ResourceID
		}
	case *RateLimitError:
		details["limit"] = e.Limit
		details["retry_after"] = e.RetryAfter
	}

	if len(details) == 0 {
		return nil
	}
	return details
}

// logError logs error with appropriate level and context
func logError(logger Logger, err error, statusCode int) {
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
	switch e := err.(type) {
	case *ValidationError:
		fields["field"] = e.Field
	case *DatabaseError:
		fields["db_operation"] = e.Operation
	case *ExternalAPIError:
		fields["service"] = e.Service
		fields["service_status"] = e.StatusCode
	case *AuthenticationError:
		fields["auth_reason"] = e.Reason
	case *NotFoundError:
		fields["resource_type"] = e.ResourceType
		fields["resource_id"] = e.ResourceID
	}

	logger.Log(level, err, fields, "HTTP error response")
}

// Middleware for error recovery and logging
func RecoveryMiddleware(logger Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					var err error
					switch v := rec.(type) {
					case error:
						err = WrapInternalError(v, "http_handler", "panic recovered")
					case string:
						err = NewInternalError("http_handler", v)
					default:
						err = NewInternalError("http_handler", "unknown panic")
					}

					logger.Log(LevelError, err, map[string]interface{}{
						"panic":       rec,
						"stack_trace": GetStackTrace(err),
						"method":      r.Method,
						"path":        r.URL.Path,
					}, "Panic recovered in HTTP handler")

					WriteHTTPError(w, err, logger)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
