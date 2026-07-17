// Package svcerr provides custom error types for consistent error handling.
//
// Error types: ValidationError, DatabaseError, ExternalAPIError, AuthenticationError,
// NotFoundError, ConflictError, RateLimitError, InternalError.
//
// All types implement ErrorWithCode interface and support error wrapping.
// A custom error type doesn't need to embed BaseError to participate -
// implementing just Coder, StackTracer, or PublicMessager independently is
// enough for the corresponding functions (GetErrorCode/HTTPStatusCode,
// GetStackTrace, and the public-message overrides) to recognize it.
//
// This package's own code imports no logging library: WriteHTTPError,
// WriteHTTPErrorHTML, and RecoveryMiddleware log through the Logger
// interface instead - pass an adapter for whatever logger the caller uses.
// (The zerologadapter subpackage, which does depend on zerolog, is a
// separate Go module nested in this repo; only importing it pulls zerolog
// into a caller's build, not this package - this module has zero
// dependencies of its own.)
//
// Classification of a joined error (errors.Join, or any tree whose
// Unwrap returns []error) follows stdlib errors.As traversal order:
// pre-order, depth-first, so the first coded error wins - for
// errors.Join, the earliest coded argument. Join(notFoundErr,
// internalErr) therefore classifies as NOT_FOUND/404 while
// Join(internalErr, notFoundErr) classifies as INTERNAL_ERROR/500.
// When aggregating errors of different severities (e.g. a client-facing
// error with an operational cleanup failure), don't rely on argument
// order - classify the aggregate explicitly:
//
//	return svcerr.Wrap(errors.Join(notFoundErr, cleanupErr),
//		svcerr.ErrCodeInternal, "request processing failed")
//
// The exported identity fields on the semantic types (ValidationError.Field,
// NotFoundError.ResourceID, RateLimitError.RetryAfter, and so on) are
// constructor outputs and must be treated as read-only: the constructors
// derive the error's code, message, and context from them once, so
// assigning one after construction desynchronizes the classification
// from what the response writers and log fields then report. The one
// identity-adjacent value legitimately learned after construction - an
// upstream retry hint - has a dedicated setter, ExternalAPIError.SetRetryAfter.
// At v1 these fields become unexported with same-name accessor methods,
// making the read-only contract compiler-enforced; migration is
// mechanical (x.Field becomes x.Field()). See docs/v1-design-pass.md.
//
// Errors are not safe for concurrent mutation. SetPublicMessage,
// SetPublicDetail, RemovePublicDetail, SetProblemType, SetProblemInstance,
// SetProblemTitle, SetAuthenticateChallenge, and RecaptureStackTrace all
// mutate the receiver in place with no locking, and the exported struct
// fields on ValidationError, DatabaseError, and the other semantic types
// are plain fields, not synchronized accessors. This is fine for the
// normal pattern of
// constructing and configuring an error locally before returning it, but
// don't call these once an error might be read or mutated from another
// goroutine (e.g. after handing it to a shared error-collection type).
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

// Coder is implemented by any error that carries an application-specific
// ErrorCode - the minimal capability GetErrorCode and HTTPStatusCode need.
// A custom error type only needs this one method to participate in status
// mapping; unlike ErrorWithCode, it doesn't also require Unwrap or
// StackTrace.
type Coder interface {
	Code() ErrorCode
}

// StackTracer is implemented by any error that can report a captured stack
// trace - the minimal capability GetStackTrace needs.
type StackTracer interface {
	StackTrace() []string
}

// PublicMessager is implemented by any error with a client-facing message
// distinct from its logged Error() text - the minimal capability
// getUserFriendlyMessage needs to honor SetPublicMessage-style overrides.
// BaseError (and so every type in this package) implements it via
// SetPublicMessage/PublicMessage.
type PublicMessager interface {
	PublicMessage() (string, bool)
}

// PublicDetailer is implemented by any error that customizes the
// structured "details" map WriteHTTPError/WriteHTTPProblem send - keys to
// add or override beyond whatever a built-in type's automatic extraction
// already contributes (or, for a code with no dedicated type, the only
// source of details at all), and keys to suppress from that automatic
// extraction (e.g. hiding NotFoundError's resource_id when the identifier
// is sensitive). BaseError implements it via SetPublicDetail/
// RemovePublicDetail.
type PublicDetailer interface {
	PublicDetails() (add map[string]interface{}, remove map[string]struct{})
}

// ProblemTyper is implemented by any error that specifies its own RFC 9457
// "type" URI for WriteHTTPProblem, in place of the default "about:blank".
// BaseError implements it via SetProblemType/ProblemType.
type ProblemTyper interface {
	ProblemType() (string, bool)
}

// ProblemInstancer is implemented by any error that specifies its own RFC
// 9457 "instance" URI for WriteHTTPProblem - e.g. a request ID or trace
// URL for this specific occurrence. BaseError implements it via
// SetProblemInstance/ProblemInstance.
type ProblemInstancer interface {
	ProblemInstance() (string, bool)
}

// ProblemTitler is implemented by any error that specifies its own RFC
// 9457 "title" for WriteHTTPProblem, in place of the default
// http.StatusText(status) - useful alongside a custom SetProblemType,
// since RFC 9457 defines title as a short, occurrence-invariant summary
// of that specific problem type, not of the HTTP status in general.
// BaseError implements it via SetProblemTitle/ProblemTitle.
type ProblemTitler interface {
	ProblemTitle() (string, bool)
}

// Authenticator is implemented by any error that specifies its own
// WWW-Authenticate challenge for a 401 response. RFC 9110 §11.6.1
// requires a server generating a 401 to include at least one
// WWW-Authenticate challenge; this package has no way to know an
// application's authentication scheme or realm on its own, so the
// response writers set one only when the error provides it - or, when it
// doesn't, from the application-wide default configured via
// SetDefaultAuthenticateChallenge (an error-specific challenge always
// wins over that default). BaseError implements it via
// SetAuthenticateChallenge/AuthenticateChallenge.
type Authenticator interface {
	AuthenticateChallenge() (string, bool)
}

// ErrorWithCode is the full capability set every type in this package
// implements via BaseError: Coder and StackTracer, plus error and Unwrap.
// Prefer the narrower Coder, StackTracer, or PublicMessager when writing a
// custom error type that doesn't want to embed BaseError - GetErrorCode,
// GetStackTrace, and getUserFriendlyMessage each check the capability they
// need independently, not this combined interface.
type ErrorWithCode interface {
	error
	Coder
	Unwrap() error
	StackTracer
}

// BaseError provides common error functionality
type BaseError struct {
	code    ErrorCode
	message string
	cause   error
	// pcs holds raw program counters from the capture site, not formatted
	// strings - StackTrace resolves and formats them lazily, since most
	// constructed errors never have StackTrace/GetStackTrace called on
	// them (errorLogFields only asks for one on a 5xx response), and
	// runtime.Callers is far cheaper than symbolizing every frame eagerly
	// on every single construction.
	pcs                   []uintptr
	context               map[string]interface{}
	publicMessage         string
	publicDetailAdditions map[string]interface{}
	publicDetailRemovals  map[string]struct{}
	problemType           string
	problemInstance       string
	problemTitle          string
	authenticateChallenge string
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

// StackTrace resolves and formats the stack trace captured when this
// error was constructed (or last RecaptureStackTrace'd). Each call builds
// a fresh []string, so there's no shared internal state for a caller to
// mutate through the result - but also no caching, so calling it
// repeatedly re-resolves the frames each time.
func (e *BaseError) StackTrace() []string {
	return formatStackTrace(e.pcs)
}

// Context returns a shallow copy of the error context: callers can't add,
// remove, or replace top-level keys in the error's internal map through
// the returned one, but a value that's itself a map, slice, or pointer is
// shared, not copied - mutating that value's contents still reaches into
// the error.
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

// SetPublicDetail adds or overrides a key in the structured "details" map
// WriteHTTPError/WriteHTTPProblem send to the client, alongside whatever
// this error's built-in type already contributes (e.g. NotFoundError's
// resource_type/resource_id) - or, for a code with no dedicated type (New/
// Wrap), the only source of details at all. Unlike SetPublicMessage, this
// can be called more than once to add several keys. Whichever of
// SetPublicDetail/RemovePublicDetail was called most recently for a given
// key wins - calling this after RemovePublicDetail(key) un-suppresses it.
//
// The two response shapes place details differently, which matters for
// six reserved names: WriteHTTPError/WriteJSON nest details under
// error.details, where any key is fine, but WriteHTTPProblem/WriteProblem
// flatten them to the top level of the RFC 9457 object, where "type",
// "title", "status", "detail", "instance", and "code" are registered (or
// package-owned) members - a detail with one of those names is silently
// omitted from problem-details output rather than allowed to occupy the
// member's slot. To set the real members, use SetProblemType,
// SetProblemTitle, and SetProblemInstance; status, detail, and code
// always come from the error's own classification.
func (e *BaseError) SetPublicDetail(key string, value interface{}) {
	delete(e.publicDetailRemovals, key)
	if e.publicDetailAdditions == nil {
		e.publicDetailAdditions = map[string]interface{}{}
	}
	e.publicDetailAdditions[key] = value
}

// RemovePublicDetail suppresses key from the structured "details" map,
// even if this error's built-in type would otherwise include it - e.g.
// hiding NotFoundError's resource_id when the identifier itself is
// sensitive (an email address, say). Whichever of SetPublicDetail/
// RemovePublicDetail was called most recently for a given key wins -
// calling this after SetPublicDetail(key, ...) un-does that addition.
func (e *BaseError) RemovePublicDetail(key string) {
	delete(e.publicDetailAdditions, key)
	if e.publicDetailRemovals == nil {
		e.publicDetailRemovals = map[string]struct{}{}
	}
	e.publicDetailRemovals[key] = struct{}{}
}

// PublicDetails returns the keys SetPublicDetail added or overrode, and
// the keys RemovePublicDetail suppressed - the capability
// extractErrorDetails needs to apply both on top of a built-in type's
// automatic extraction. Both returned maps are shallow copies, the same
// contract as Context(): adding or removing keys through them doesn't
// reach back into the error - use SetPublicDetail/RemovePublicDetail,
// which also keep the two maps' last-call-wins bookkeeping intact - but
// an addition value that's itself a map, slice, or pointer is shared,
// not copied.
func (e *BaseError) PublicDetails() (add map[string]interface{}, remove map[string]struct{}) {
	// maps.Clone would do this, but it was added in Go 1.21 and this
	// module's floor is Go 1.20.
	if e.publicDetailAdditions != nil {
		add = make(map[string]interface{}, len(e.publicDetailAdditions))
		for k, v := range e.publicDetailAdditions {
			add[k] = v
		}
	}
	if e.publicDetailRemovals != nil {
		remove = make(map[string]struct{}, len(e.publicDetailRemovals))
		for k := range e.publicDetailRemovals {
			remove[k] = struct{}{}
		}
	}
	return add, remove
}

// SetProblemType overrides the RFC 9457 "type" URI WriteHTTPProblem sends
// for this error, in place of the default "about:blank". Per RFC 9457,
// pair this with SetProblemTitle too - a stable, occurrence-invariant
// summary of the custom type, since http.StatusText(status) (the default
// Title, correct only for "about:blank") describes the HTTP status in
// general, not this specific problem type.
func (e *BaseError) SetProblemType(uri string) {
	e.problemType = uri
}

// ProblemType returns the URI set by SetProblemType, and whether one was
// set at all.
func (e *BaseError) ProblemType() (string, bool) {
	return e.problemType, e.problemType != ""
}

// SetProblemInstance sets the RFC 9457 "instance" URI WriteHTTPProblem
// sends for this specific occurrence - e.g. a request ID or trace URL.
// Unset by default, in which case the field is omitted entirely.
func (e *BaseError) SetProblemInstance(uri string) {
	e.problemInstance = uri
}

// ProblemInstance returns the URI set by SetProblemInstance, and whether
// one was set at all.
func (e *BaseError) ProblemInstance() (string, bool) {
	return e.problemInstance, e.problemInstance != ""
}

// SetProblemTitle overrides the RFC 9457 "title" WriteHTTPProblem sends
// for this error, in place of the default http.StatusText(status). See
// SetProblemType.
func (e *BaseError) SetProblemTitle(title string) {
	e.problemTitle = title
}

// ProblemTitle returns the title set by SetProblemTitle, and whether one
// was set at all.
func (e *BaseError) ProblemTitle() (string, bool) {
	return e.problemTitle, e.problemTitle != ""
}

// SetAuthenticateChallenge sets the WWW-Authenticate header
// WriteHTTPError/WriteHTTPErrorHTML/WriteHTTPProblem send alongside a 401
// response - e.g. `Bearer realm="api"`. Only applied when the error's
// code maps to 401; set on an error whose code maps elsewhere and it's
// silently unused. Takes precedence over the application-wide
// SetDefaultAuthenticateChallenge value, which covers 401 errors that
// don't call this.
func (e *BaseError) SetAuthenticateChallenge(challenge string) {
	e.authenticateChallenge = challenge
}

// AuthenticateChallenge returns the challenge set by
// SetAuthenticateChallenge, and whether one was set at all.
func (e *BaseError) AuthenticateChallenge() (string, bool) {
	return e.authenticateChallenge, e.authenticateChallenge != ""
}

// ownMessage returns e.message alone, never a wrapped cause's text -
// unlike Error(), which appends the cause when one is set. Every
// constructor in this package (New, Wrap, and every semantic New*/Wrap*
// pair) takes message as an explicit caller-supplied argument rather than
// deriving it from a wrapped error, so this is always safe to treat as
// caller-controlled text regardless of whether the error wraps a cause.
func (e *BaseError) ownMessage() string {
	return e.message
}

// Compile-time checks that BaseError - and so every type in this package,
// which all embed it - satisfies each capability interface individually as
// well as their combination.
var (
	_ Coder            = (*BaseError)(nil)
	_ StackTracer      = (*BaseError)(nil)
	_ PublicMessager   = (*BaseError)(nil)
	_ PublicDetailer   = (*BaseError)(nil)
	_ ProblemTyper     = (*BaseError)(nil)
	_ ProblemInstancer = (*BaseError)(nil)
	_ ProblemTitler    = (*BaseError)(nil)
	_ Authenticator    = (*BaseError)(nil)
	_ ErrorWithCode    = (*BaseError)(nil)
)

// stackPathSegments is the number of trailing path segments kept when
// shortening a stack frame's file path (e.g. "internal/errors/http.go").
const stackPathSegments = 3

// maxStackFrames caps how many frames captureStackTrace records - matches
// the old runtime.Caller loop's fixed 10-frame bound.
const maxStackFrames = 10

// captureStackTrace records raw program counters for the current call
// stack via a single runtime.Callers call. Resolving them into the
// formatted "file:line func" strings StackTrace/GetStackTrace return is
// deferred to formatStackTrace, since most constructed errors never have
// that called on them (errorLogFields only includes a stack trace for a
// 5xx response) - runtime.Callers is far cheaper than symbolizing every
// frame with runtime.FuncForPC on every single construction, most of
// which pay that cost for nothing.
func captureStackTrace(skip int) []uintptr {
	pcs := make([]uintptr, maxStackFrames)
	// runtime.Callers' skip counts its own frame as 0 - one more than
	// runtime.Caller's "0 identifies the caller of Caller", which already
	// excludes Caller's own frame. skip+1 here lands on the same frame
	// skip did under the old runtime.Caller-loop implementation, so every
	// New*/Wrap* call site passing captureStackTrace(2) is unaffected.
	n := runtime.Callers(skip+1, pcs)
	return pcs[:n]
}

// formatStackTrace resolves pcs into the shortened "file:line func" string
// form StackTrace/GetStackTrace return.
func formatStackTrace(pcs []uintptr) []string {
	if len(pcs) == 0 {
		return nil
	}
	stack := make([]string, 0, len(pcs))
	frames := runtime.CallersFrames(pcs)
	for {
		frame, more := frames.Next()
		// Shorten file path for readability: keep only the trailing
		// segments rather than the full absolute path, since this
		// package has no way to know the caller's repo layout.
		file := frame.File
		parts := strings.Split(file, "/")
		if len(parts) > stackPathSegments {
			file = strings.Join(parts[len(parts)-stackPathSegments:], "/")
		}
		stack = append(stack, fmt.Sprintf("%s:%d %s", file, frame.Line, frame.Function))
		if !more {
			break
		}
	}
	return stack
}

// setStackTrace lets RecaptureStackTrace overwrite the trace captured at
// construction time.
func (e *BaseError) setStackTrace(pcs []uintptr) {
	e.pcs = pcs
}

// stackTraceSetter is implemented by every BaseError-derived type via
// promotion; RecaptureStackTrace checks it through errors.As.
type stackTraceSetter interface {
	setStackTrace([]uintptr)
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
		code:    code,
		message: message,
		pcs:     captureStackTrace(2),
	}
}

// Wrap wraps err as a generic error with the given code and message. As
// with the semantic Wrap* constructors, err's text is never shown to
// clients unless SetPublicMessage is called explicitly.
func Wrap(err error, code ErrorCode, message string) *BaseError {
	return &BaseError{
		code:    code,
		message: message,
		cause:   err,
		pcs:     captureStackTrace(2),
	}
}

// ValidationError represents input validation errors
type ValidationError struct {
	BaseError
	// Read-only identity fields - see the package doc's identity-field
	// note; same-name accessor methods replace them at v1.
	Field string
	Value interface{}
}

// NewValidationError creates a new validation error
func NewValidationError(message string, field string, value interface{}) *ValidationError {
	return &ValidationError{
		BaseError: BaseError{
			code:    ErrCodeInvalidInput,
			message: message,
			pcs:     captureStackTrace(2),
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
			code:    ErrCodeInvalidInput,
			message: message,
			cause:   err,
			pcs:     captureStackTrace(2),
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
	// Read-only identity fields - see the package doc's identity-field
	// note; same-name accessor methods replace them at v1.
	Operation string // "query", "insert", "update", "delete", "transaction", "migration"
	Query     string
}

// databaseErrorCode maps a DatabaseError's Operation to its ErrorCode -
// ErrCodeDatabaseTransaction/ErrCodeDatabaseMigration for those two
// specific operations (both still map to the same 500 HTTPStatusCode as
// ErrCodeDatabaseQuery, but the distinct code lets a caller branch on it,
// or override its status independently via RegisterStatusCode), and
// ErrCodeDatabaseQuery for everything else ("query", "insert", "update",
// "delete", or any operation string not recognized here).
func databaseErrorCode(operation string) ErrorCode {
	switch operation {
	case "transaction":
		return ErrCodeDatabaseTransaction
	case "migration":
		return ErrCodeDatabaseMigration
	default:
		return ErrCodeDatabaseQuery
	}
}

// NewDatabaseError creates a new database error
func NewDatabaseError(operation, message string) *DatabaseError {
	return &DatabaseError{
		BaseError: BaseError{
			code:    databaseErrorCode(operation),
			message: message,
			pcs:     captureStackTrace(2),
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
			code:    databaseErrorCode(operation),
			message: fmt.Sprintf("database %s failed", operation),
			cause:   err,
			pcs:     captureStackTrace(2),
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
	// Read-only identity fields - see the package doc's identity-field
	// note; same-name accessor methods replace them at v1.
	Service    string // caller-defined service name, e.g. "yahoo", "nba_stats"
	StatusCode int
	URL        string
	// RetryAfter is seconds to retry after, e.g. propagated from the
	// upstream's own Retry-After. Not set by the constructors - use
	// SetRetryAfter when the hint is known, which clamps to non-negative
	// (RFC 9110 §10.2.3); direct assignment bypasses that clamp and is
	// deprecated ahead of v1, when this field becomes unexported. When
	// set, the response writers emit it as the Retry-After header and the
	// retry_after details member, re-clamped at emission.
	RetryAfter *int
}

// NewExternalAPIError creates a new external API error
func NewExternalAPIError(service, message string, statusCode int, url string) *ExternalAPIError {
	return &ExternalAPIError{
		BaseError: BaseError{
			code:    ErrCodeExternalAPI,
			message: message,
			pcs:     captureStackTrace(2),
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
			code:    ErrCodeExternalAPI,
			message: fmt.Sprintf("%s API call failed", service),
			cause:   err,
			pcs:     captureStackTrace(2),
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

// SetRetryAfter records an upstream retry hint of seconds (e.g. parsed
// from the upstream's own Retry-After), clamped to non-negative per RFC
// 9110 §10.2.3 - the sanctioned way to attach the hint after
// construction, since no constructor takes it. The response writers then
// emit it as the Retry-After header and the retry_after details member.
func (e *ExternalAPIError) SetRetryAfter(seconds int) {
	seconds = clampRetryAfter(seconds)
	e.RetryAfter = &seconds
}

// AuthenticationError represents authentication and authorization errors
type AuthenticationError struct {
	BaseError
	// Read-only identity fields - see the package doc's identity-field
	// note; same-name accessor methods replace them at v1.
	SessionID string
	Reason    string // "token_expired", "token_invalid", "permission_denied"
}

// authenticationErrorCode maps an AuthenticationError's Reason to its
// ErrorCode - ErrCodeTokenExpired/ErrCodeTokenInvalid/
// ErrCodePermissionDenied for those three specific reasons, and
// ErrCodeUnauthorized for everything else. Mirrors databaseErrorCode's
// role for DatabaseError, and exists for the same reason: a New*/Wrap*
// pair sharing mapping logic shouldn't each carry its own copy of it.
func authenticationErrorCode(reason string) ErrorCode {
	switch reason {
	case "token_expired":
		return ErrCodeTokenExpired
	case "token_invalid":
		return ErrCodeTokenInvalid
	case "permission_denied":
		return ErrCodePermissionDenied
	default:
		return ErrCodeUnauthorized
	}
}

// NewAuthenticationError creates a new authentication error
func NewAuthenticationError(reason, message string) *AuthenticationError {
	return &AuthenticationError{
		BaseError: BaseError{
			code:    authenticationErrorCode(reason),
			message: message,
			pcs:     captureStackTrace(2),
			context: map[string]interface{}{
				"reason": reason,
			},
		},
		Reason: reason,
	}
}

// WrapAuthenticationError wraps an existing error as an authentication
// error. ErrCodeUnauthorized/ErrCodeTokenExpired/ErrCodeTokenInvalid/
// ErrCodePermissionDenied are all in mayExposeOwnMessage's safe category,
// so message is shown to the client the same as NewAuthenticationError's -
// it's still an explicit caller argument, never derived from err.
func WrapAuthenticationError(err error, reason, message string) *AuthenticationError {
	return &AuthenticationError{
		BaseError: BaseError{
			code:    authenticationErrorCode(reason),
			message: message,
			cause:   err,
			pcs:     captureStackTrace(2),
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
	// Read-only identity fields - see the package doc's identity-field
	// note; same-name accessor methods replace them at v1.
	ResourceType string
	ResourceID   string
}

// NewNotFoundError creates a new not found error
func NewNotFoundError(resourceType, resourceID string) *NotFoundError {
	return &NotFoundError{
		BaseError: BaseError{
			code:    ErrCodeNotFound,
			message: fmt.Sprintf("%s not found: %s", resourceType, resourceID),
			pcs:     captureStackTrace(2),
			context: map[string]interface{}{
				"resource_type": resourceType,
				"resource_id":   resourceID,
			},
		},
		ResourceType: resourceType,
		ResourceID:   resourceID,
	}
}

// WrapNotFoundError wraps an existing error (e.g. sql.ErrNoRows) as a not
// found error, preserving it in the chain for errors.Is/errors.As while
// still getting NotFoundError's type, automatic resource_type/resource_id
// details, and client-facing message - which is generated the same way
// NewNotFoundError's is, never derived from err's own text.
func WrapNotFoundError(err error, resourceType, resourceID string) *NotFoundError {
	return &NotFoundError{
		BaseError: BaseError{
			code:    ErrCodeNotFound,
			message: fmt.Sprintf("%s not found: %s", resourceType, resourceID),
			cause:   err,
			pcs:     captureStackTrace(2),
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
	// Read-only identity fields - see the package doc's identity-field
	// note; same-name accessor methods replace them at v1.
	ResourceType string
	ConflictKey  string
}

// NewConflictError creates a new conflict error
func NewConflictError(resourceType, conflictKey, message string) *ConflictError {
	return &ConflictError{
		BaseError: BaseError{
			code:    ErrCodeAlreadyExists,
			message: message,
			pcs:     captureStackTrace(2),
			context: map[string]interface{}{
				"resource_type": resourceType,
				"conflict_key":  conflictKey,
			},
		},
		ResourceType: resourceType,
		ConflictKey:  conflictKey,
	}
}

// WrapConflictError wraps an existing error as a conflict error.
// ErrCodeAlreadyExists is in mayExposeOwnMessage's safe category, so
// message is shown to the client the same as NewConflictError's - it's
// still an explicit caller argument, never derived from err.
func WrapConflictError(err error, resourceType, conflictKey, message string) *ConflictError {
	return &ConflictError{
		BaseError: BaseError{
			code:    ErrCodeAlreadyExists,
			message: message,
			cause:   err,
			pcs:     captureStackTrace(2),
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
	// Read-only identity fields - see the package doc's identity-field
	// note; same-name accessor methods replace them at v1.
	Service    string
	Limit      int
	RetryAfter int // seconds
}

// clampRetryAfter floors retryAfter at 0. RFC 9110 §10.2.3 defines
// Retry-After as a non-negative delay-seconds (or an HTTP-date), so a
// negative value is never valid on the wire. It's applied twice: at
// construction, so the stored RetryAfter field and the "retry_after"
// context entry start out valid, and again at emission
// (retryAfterHeader, extractErrorDetails, errorLogFields) -
// RetryAfter is an exported, writable field, so the construction-time
// clamp is input cleanup, not an enforced invariant, and a negative
// value assigned after construction would otherwise go straight into the
// header via fmt.Sprintf("%d", ...) with no downstream validation.
func clampRetryAfter(retryAfter int) int {
	if retryAfter < 0 {
		return 0
	}
	return retryAfter
}

// NewRateLimitError creates a new rate limit error
func NewRateLimitError(service string, limit, retryAfter int) *RateLimitError {
	retryAfter = clampRetryAfter(retryAfter)
	return &RateLimitError{
		BaseError: BaseError{
			code:    ErrCodeRateLimitExceeded,
			message: fmt.Sprintf("rate limit exceeded for %s: %d requests", service, limit),
			pcs:     captureStackTrace(2),
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

// WrapRateLimitError wraps an existing error as a rate limit error.
// ErrCodeRateLimitExceeded is in mayExposeOwnMessage's safe category, so
// message is generated the same way NewRateLimitError's is, never derived
// from err's own text.
func WrapRateLimitError(err error, service string, limit, retryAfter int) *RateLimitError {
	retryAfter = clampRetryAfter(retryAfter)
	return &RateLimitError{
		BaseError: BaseError{
			code:    ErrCodeRateLimitExceeded,
			message: fmt.Sprintf("rate limit exceeded for %s: %d requests", service, limit),
			cause:   err,
			pcs:     captureStackTrace(2),
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
	// Read-only identity fields - see the package doc's identity-field
	// note; same-name accessor methods replace them at v1.
	Component string
}

// NewInternalError creates a new internal error
func NewInternalError(component, message string) *InternalError {
	return &InternalError{
		BaseError: BaseError{
			code:    ErrCodeInternal,
			message: message,
			pcs:     captureStackTrace(2),
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
			code:    ErrCodeInternal,
			message: message,
			cause:   err,
			pcs:     captureStackTrace(2),
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

// coderError pairs error with Coder for outermostCoded's errors.As search.
// Every element of an error chain already satisfies error (that's what
// Unwrap returns), so requiring it here doesn't narrow the search beyond
// what a plain Coder target would already match.
type coderError interface {
	error
	Coder
}

// outermostCoded returns the first error in err's chain that carries an
// application ErrorCode - the same node GetErrorCode's return value comes
// from. "First" is errors.As traversal order: pre-order, depth-first, so
// for a joined error (errors.Join, or any Unwrap() []error tree) the
// earliest coded child wins - see the package doc comment for why callers
// aggregating errors of different severities should classify the
// aggregate explicitly instead of relying on that order. Callers that need type-specific data (a validation field, a
// retry-after value, a resource ID, ...) should derive it from this one
// node rather than independently re-scanning the whole chain with their
// own errors.As call. Otherwise an outer wrapper's code (e.g.
// ErrCodeInternal from WrapInternalError) can end up paired with a wrapped
// error's details (e.g. a NotFoundError's resource ID), leaking structured
// data that the wrapping was meant to hide.
func outermostCoded(err error) coderError {
	var c coderError
	if errors.As(err, &c) {
		return c
	}
	return nil
}

// ownMessager is implemented by every BaseError-derived type via
// promotion; getUserFriendlyMessage checks it through a type assertion on
// outermostCoded's result rather than errors.As, since it only ever wants
// the outermost node's own message, never a deeper one in the chain.
type ownMessager interface {
	ownMessage() string
}

// GetErrorCode extracts the error code from an error. It only requires
// Coder, not the full ErrorWithCode - a custom error type that implements
// just Code() ErrorCode is picked up here (and so by HTTPStatusCode) even
// if it doesn't also implement Unwrap or StackTrace.
func GetErrorCode(err error) ErrorCode {
	if node := outermostCoded(err); node != nil {
		return node.Code()
	}
	return ErrCodeInternal
}

// GetStackTrace extracts the stack trace from an error. It only requires
// StackTracer, not the full ErrorWithCode.
func GetStackTrace(err error) []string {
	var st StackTracer
	if errors.As(err, &st) {
		return st.StackTrace()
	}
	return nil
}
