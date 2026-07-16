// Package svcerr provides custom error types for consistent error handling.
//
// Error types: ValidationError, DatabaseError, ExternalAPIError, AuthenticationError,
// NotFoundError, ConflictError, RateLimitError, InternalError.
//
// All types implement ErrorWithCode interface and support error wrapping.
//
// This package's own code imports no logging library: WriteHTTPError,
// WriteHTTPErrorHTML, and RecoveryMiddleware log through the Logger
// interface instead - pass an adapter for whatever logger the caller uses.
// (The zerologadapter subpackage, which does depend on zerolog, is optional
// and lives in this same module; importing it is what pulls zerolog into a
// caller's build, not this package.)
package svcerr

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// ErrorCode represents application-specific error codes
type ErrorCode string

const (
	// Validation errors (1xxx)
	ErrCodeInvalidInput        ErrorCode = "INVALID_INPUT"
	ErrCodeMissingRequired     ErrorCode = "MISSING_REQUIRED"
	ErrCodeInvalidFormat       ErrorCode = "INVALID_FORMAT"
	ErrCodeConstraintViolation ErrorCode = "CONSTRAINT_VIOLATION"

	// Database errors (2xxx)
	ErrCodeDatabaseConnection  ErrorCode = "DB_CONNECTION"
	ErrCodeDatabaseQuery       ErrorCode = "DB_QUERY"
	ErrCodeDatabaseTransaction ErrorCode = "DB_TRANSACTION"
	ErrCodeDatabaseMigration   ErrorCode = "DB_MIGRATION"

	// External API errors (3xxx). The specific service is carried in
	// ExternalAPIError.Service, not encoded as a separate code per service.
	ErrCodeExternalAPI ErrorCode = "EXTERNAL_API_ERROR"

	// Authentication errors (4xxx)
	ErrCodeUnauthorized     ErrorCode = "UNAUTHORIZED"
	ErrCodeTokenExpired     ErrorCode = "TOKEN_EXPIRED"
	ErrCodeTokenInvalid     ErrorCode = "TOKEN_INVALID"
	ErrCodePermissionDenied ErrorCode = "PERMISSION_DENIED"

	// Resource errors (5xxx)
	ErrCodeNotFound         ErrorCode = "NOT_FOUND"
	ErrCodeAlreadyExists    ErrorCode = "ALREADY_EXISTS"
	ErrCodeResourceConflict ErrorCode = "RESOURCE_CONFLICT"

	// Rate limiting (6xxx)
	ErrCodeRateLimitExceeded ErrorCode = "RATE_LIMIT_EXCEEDED"
	ErrCodeQuotaExceeded     ErrorCode = "QUOTA_EXCEEDED"

	// Internal errors (9xxx). RecoveryMiddleware reports recovered panics
	// as ErrCodeInternal too - there's no separate panic-specific code.
	ErrCodeInternal       ErrorCode = "INTERNAL_ERROR"
	ErrCodeNotImplemented ErrorCode = "NOT_IMPLEMENTED"
)

// ErrorWithCode interface for errors that have application-specific codes
type ErrorWithCode interface {
	error
	Code() ErrorCode
	Unwrap() error
	StackTrace() []string
}

// BaseError provides common error functionality
type BaseError struct {
	code          ErrorCode
	message       string
	cause         error
	stackTrace    []string
	context       map[string]interface{}
	publicMessage string
}

// Code returns the error code
func (e *BaseError) Code() ErrorCode {
	return e.code
}

// Error implements the error interface
func (e *BaseError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.message, e.cause)
	}
	return e.message
}

// Unwrap implements error unwrapping
func (e *BaseError) Unwrap() error {
	return e.cause
}

// StackTrace returns a copy of the captured stack trace - callers can't
// mutate the error's internal state through the returned slice.
func (e *BaseError) StackTrace() []string {
	if e.stackTrace == nil {
		return nil
	}
	stack := make([]string, len(e.stackTrace))
	copy(stack, e.stackTrace)
	return stack
}

// Context returns a copy of the error context - callers can't mutate the
// error's internal state through the returned map.
func (e *BaseError) Context() map[string]interface{} {
	if e.context == nil {
		return nil
	}
	ctx := make(map[string]interface{}, len(e.context))
	for k, v := range e.context {
		ctx[k] = v
	}
	return ctx
}

// SetPublicMessage overrides the message WriteHTTPError, WriteHTTPErrorHTML,
// and UserMessage show the client for this error instance, so the logged
// Error() text (which may carry internal detail) and the client-facing text
// can differ. Unset by default, in which case those functions fall back to
// their normal behavior (the error's own message, or a default per-code
// message).
func (e *BaseError) SetPublicMessage(msg string) {
	e.publicMessage = msg
}

// PublicMessage returns the message set by SetPublicMessage, and whether
// one was set at all.
func (e *BaseError) PublicMessage() (string, bool) {
	return e.publicMessage, e.publicMessage != ""
}

// publicMessager is implemented by every BaseError-derived type via
// promotion; getUserFriendlyMessage checks it through errors.As.
type publicMessager interface {
	PublicMessage() (string, bool)
}

// stackPathSegments is the number of trailing path segments kept when
// shortening a stack frame's file path (e.g. "internal/errors/http.go").
const stackPathSegments = 3

// captureStackTrace captures the current stack trace
func captureStackTrace(skip int) []string {
	var stack []string
	for i := skip; i < skip+10; i++ {
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}
		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}
		// Shorten file path for readability: keep only the trailing
		// segments rather than the full absolute path, since this
		// package has no way to know the caller's repo layout.
		parts := strings.Split(file, "/")
		if len(parts) > stackPathSegments {
			file = strings.Join(parts[len(parts)-stackPathSegments:], "/")
		}
		stack = append(stack, fmt.Sprintf("%s:%d %s", file, line, fn.Name()))
	}
	return stack
}

// setStackTrace lets RecaptureStackTrace overwrite the trace captured at
// construction time.
func (e *BaseError) setStackTrace(s []string) {
	e.stackTrace = s
}

// stackTraceSetter is implemented by every BaseError-derived type via
// promotion; RecaptureStackTrace checks it through errors.As.
type stackTraceSetter interface {
	setStackTrace([]string)
}

// RecaptureStackTrace re-captures err's stack trace starting extraSkip
// frames higher than the normal New*/Wrap* capture point. Every
// constructor in this package assumes it's called directly from the site
// the trace should point at; if you wrap a constructor in your own helper
// function, the trace ends up pointing at that helper instead of its
// caller. Call RecaptureStackTrace(err, 1) from inside such a helper,
// immediately after constructing err, to fix that - err must be one of
// this package's error types (or wrap one); otherwise this is a no-op.
func RecaptureStackTrace(err error, extraSkip int) {
	var setter stackTraceSetter
	if !errors.As(err, &setter) {
		return
	}
	setter.setStackTrace(captureStackTrace(2 + extraSkip))
}

// New creates a generic error with the given code and message. Prefer the
// semantic constructors below (NewValidationError, NewNotFoundError, ...)
// when one exists for what you're representing; use New directly for codes
// that have no dedicated constructor, e.g. ErrCodeMissingRequired,
// ErrCodeDatabaseConnection, ErrCodeDatabaseTransaction,
// ErrCodeDatabaseMigration, ErrCodeResourceConflict, or ErrCodeQuotaExceeded.
func New(code ErrorCode, message string) *BaseError {
	return &BaseError{
		code:       code,
		message:    message,
		stackTrace: captureStackTrace(2),
	}
}

// Wrap wraps err as a generic error with the given code and message. As
// with the semantic Wrap* constructors, err's text is never shown to
// clients unless SetPublicMessage is called explicitly.
func Wrap(err error, code ErrorCode, message string) *BaseError {
	return &BaseError{
		code:       code,
		message:    message,
		cause:      err,
		stackTrace: captureStackTrace(2),
	}
}

// ValidationError represents input validation errors
type ValidationError struct {
	BaseError
	Field string
	Value interface{}
}

// NewValidationError creates a new validation error
func NewValidationError(message string, field string, value interface{}) *ValidationError {
	return &ValidationError{
		BaseError: BaseError{
			code:       ErrCodeInvalidInput,
			message:    message,
			stackTrace: captureStackTrace(2),
			context: map[string]interface{}{
				"field": field,
				"value": value,
			},
		},
		Field: field,
		Value: value,
	}
}

// WrapValidationError wraps an existing error as a validation error
func WrapValidationError(err error, message string, field string) *ValidationError {
	return &ValidationError{
		BaseError: BaseError{
			code:       ErrCodeInvalidInput,
			message:    message,
			cause:      err,
			stackTrace: captureStackTrace(2),
			context: map[string]interface{}{
				"field": field,
			},
		},
		Field: field,
	}
}

// DatabaseError represents database operation errors
type DatabaseError struct {
	BaseError
	Operation string // "query", "insert", "update", "delete", "transaction"
	Query     string
}

// NewDatabaseError creates a new database error
func NewDatabaseError(operation, message string) *DatabaseError {
	return &DatabaseError{
		BaseError: BaseError{
			code:       ErrCodeDatabaseQuery,
			message:    message,
			stackTrace: captureStackTrace(2),
			context: map[string]interface{}{
				"operation": operation,
			},
		},
		Operation: operation,
	}
}

// WrapDatabaseError wraps an existing error as a database error
func WrapDatabaseError(err error, operation, query string) *DatabaseError {
	return &DatabaseError{
		BaseError: BaseError{
			code:       ErrCodeDatabaseQuery,
			message:    fmt.Sprintf("database %s failed", operation),
			cause:      err,
			stackTrace: captureStackTrace(2),
			context: map[string]interface{}{
				"operation": operation,
				"query":     query,
			},
		},
		Operation: operation,
		Query:     query,
	}
}

// ExternalAPIError represents errors from external APIs
type ExternalAPIError struct {
	BaseError
	Service    string // caller-defined service name, e.g. "yahoo", "nba_stats"
	StatusCode int
	URL        string
	RetryAfter *int // seconds to retry after; not set by the constructors, assign it directly when known
}

// NewExternalAPIError creates a new external API error
func NewExternalAPIError(service, message string, statusCode int, url string) *ExternalAPIError {
	return &ExternalAPIError{
		BaseError: BaseError{
			code:       ErrCodeExternalAPI,
			message:    message,
			stackTrace: captureStackTrace(2),
			context: map[string]interface{}{
				"service":     service,
				"status_code": statusCode,
				"url":         url,
			},
		},
		Service:    service,
		StatusCode: statusCode,
		URL:        url,
	}
}

// WrapExternalAPIError wraps an existing error as an external API error
func WrapExternalAPIError(err error, service, url string, statusCode int) *ExternalAPIError {
	return &ExternalAPIError{
		BaseError: BaseError{
			code:       ErrCodeExternalAPI,
			message:    fmt.Sprintf("%s API call failed", service),
			cause:      err,
			stackTrace: captureStackTrace(2),
			context: map[string]interface{}{
				"service":     service,
				"url":         url,
				"status_code": statusCode,
			},
		},
		Service:    service,
		StatusCode: statusCode,
		URL:        url,
	}
}

// AuthenticationError represents authentication and authorization errors
type AuthenticationError struct {
	BaseError
	SessionID string
	Reason    string // "token_expired", "token_invalid", "permission_denied"
}

// NewAuthenticationError creates a new authentication error
func NewAuthenticationError(reason, message string) *AuthenticationError {
	code := ErrCodeUnauthorized
	switch reason {
	case "token_expired":
		code = ErrCodeTokenExpired
	case "token_invalid":
		code = ErrCodeTokenInvalid
	case "permission_denied":
		code = ErrCodePermissionDenied
	}

	return &AuthenticationError{
		BaseError: BaseError{
			code:       code,
			message:    message,
			stackTrace: captureStackTrace(2),
			context: map[string]interface{}{
				"reason": reason,
			},
		},
		Reason: reason,
	}
}

// NotFoundError represents resource not found errors
type NotFoundError struct {
	BaseError
	ResourceType string
	ResourceID   string
}

// NewNotFoundError creates a new not found error
func NewNotFoundError(resourceType, resourceID string) *NotFoundError {
	return &NotFoundError{
		BaseError: BaseError{
			code:       ErrCodeNotFound,
			message:    fmt.Sprintf("%s not found: %s", resourceType, resourceID),
			stackTrace: captureStackTrace(2),
			context: map[string]interface{}{
				"resource_type": resourceType,
				"resource_id":   resourceID,
			},
		},
		ResourceType: resourceType,
		ResourceID:   resourceID,
	}
}

// ConflictError represents resource conflict errors
type ConflictError struct {
	BaseError
	ResourceType string
	ConflictKey  string
}

// NewConflictError creates a new conflict error
func NewConflictError(resourceType, conflictKey, message string) *ConflictError {
	return &ConflictError{
		BaseError: BaseError{
			code:       ErrCodeAlreadyExists,
			message:    message,
			stackTrace: captureStackTrace(2),
			context: map[string]interface{}{
				"resource_type": resourceType,
				"conflict_key":  conflictKey,
			},
		},
		ResourceType: resourceType,
		ConflictKey:  conflictKey,
	}
}

// RateLimitError represents rate limiting errors
type RateLimitError struct {
	BaseError
	Service    string
	Limit      int
	RetryAfter int // seconds
}

// NewRateLimitError creates a new rate limit error
func NewRateLimitError(service string, limit, retryAfter int) *RateLimitError {
	return &RateLimitError{
		BaseError: BaseError{
			code:       ErrCodeRateLimitExceeded,
			message:    fmt.Sprintf("rate limit exceeded for %s: %d requests", service, limit),
			stackTrace: captureStackTrace(2),
			context: map[string]interface{}{
				"service":     service,
				"limit":       limit,
				"retry_after": retryAfter,
			},
		},
		Service:    service,
		Limit:      limit,
		RetryAfter: retryAfter,
	}
}

// InternalError represents unexpected internal errors
type InternalError struct {
	BaseError
	Component string
}

// NewInternalError creates a new internal error
func NewInternalError(component, message string) *InternalError {
	return &InternalError{
		BaseError: BaseError{
			code:       ErrCodeInternal,
			message:    message,
			stackTrace: captureStackTrace(2),
			context: map[string]interface{}{
				"component": component,
			},
		},
		Component: component,
	}
}

// WrapInternalError wraps an existing error as an internal error
func WrapInternalError(err error, component, message string) *InternalError {
	return &InternalError{
		BaseError: BaseError{
			code:       ErrCodeInternal,
			message:    message,
			cause:      err,
			stackTrace: captureStackTrace(2),
			context: map[string]interface{}{
				"component": component,
			},
		},
		Component: component,
	}
}

// Helper functions for error checking
//
// Type-specific checks (e.g. "is this a ValidationError?") aren't provided
// here - use stdlib errors.As(err, &target) directly, which does the same
// thing without a per-type wrapper to maintain.

// GetErrorCode extracts the error code from an error
func GetErrorCode(err error) ErrorCode {
	var ewc ErrorWithCode
	if errors.As(err, &ewc) {
		return ewc.Code()
	}
	return ErrCodeInternal
}

// GetStackTrace extracts the stack trace from an error
func GetStackTrace(err error) []string {
	var ewc ErrorWithCode
	if errors.As(err, &ewc) {
		return ewc.StackTrace()
	}
	return nil
}
