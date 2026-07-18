package svcerr

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestValidationError(t *testing.T) {
	tests := []struct {
		name      string
		field     string
		value     any
		wantCode  ErrorCode
		wantField string
	}{
		{
			name:      "new validation error",
			field:     "email",
			value:     "invalid-email",
			wantCode:  ErrCodeInvalidInput,
			wantField: "email",
		},
		{
			name:      "missing required field",
			field:     "team_id",
			value:     nil,
			wantCode:  ErrCodeInvalidInput,
			wantField: "team_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewValidationError("test error", tt.field, tt.value)

			if err.Code() != tt.wantCode {
				t.Errorf("Code() = %v, want %v", err.Code(), tt.wantCode)
			}

			if err.Field() != tt.wantField {
				t.Errorf("Field = %v, want %v", err.Field(), tt.wantField)
			}

			if err.Error() == "" {
				t.Error("Error() returned empty string")
			}

			// Check stack trace was captured
			if len(err.StackTrace()) == 0 {
				t.Error("StackTrace() is empty")
			}
		})
	}
}

func TestWrapValidationError(t *testing.T) {
	originalErr := errors.New("parse error")
	wrappedErr := WrapValidationError(originalErr, "failed to parse input", "user_id")

	if wrappedErr.Code() != ErrCodeInvalidInput {
		t.Errorf("Code() = %v, want %v", wrappedErr.Code(), ErrCodeInvalidInput)
	}

	if wrappedErr.Field() != "user_id" {
		t.Errorf("Field = %v, want user_id", wrappedErr.Field())
	}

	// Check error wrapping works
	if !errors.Is(wrappedErr, originalErr) {
		t.Error("errors.Is() failed, wrapping not working")
	}

	unwrapped := wrappedErr.Unwrap()
	if unwrapped != originalErr {
		t.Error("Unwrap() did not return original error")
	}
}

func TestDatabaseError(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		query     string
		wantCode  ErrorCode
	}{
		{
			name:      "query error",
			operation: "query",
			query:     "SELECT * FROM users",
			wantCode:  ErrCodeDatabaseQuery,
		},
		{
			name:      "insert error",
			operation: "insert",
			query:     "INSERT INTO users...",
			wantCode:  ErrCodeDatabaseQuery,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewDatabaseError(tt.operation, "database error")

			if err.Code() != tt.wantCode {
				t.Errorf("Code() = %v, want %v", err.Code(), tt.wantCode)
			}

			if err.Operation() != tt.operation {
				t.Errorf("Operation = %v, want %v", err.Operation(), tt.operation)
			}
		})
	}
}

func TestWrapDatabaseError(t *testing.T) {
	originalErr := errors.New("connection refused")
	wrappedErr := WrapDatabaseError(originalErr, "query", "SELECT * FROM leagues")

	if wrappedErr.Code() != ErrCodeDatabaseQuery {
		t.Errorf("Code() = %v, want %v", wrappedErr.Code(), ErrCodeDatabaseQuery)
	}

	if wrappedErr.Operation() != "query" {
		t.Errorf("Operation = %v, want query", wrappedErr.Operation())
	}

	if wrappedErr.Query() != "SELECT * FROM leagues" {
		t.Errorf("Query = %v, want SELECT * FROM leagues", wrappedErr.Query())
	}

	if !errors.Is(wrappedErr, originalErr) {
		t.Error("errors.Is() failed")
	}
}

func TestExternalAPIError(t *testing.T) {
	tests := []struct {
		name       string
		service    string
		statusCode int
		url        string
		wantCode   ErrorCode
	}{
		{
			name:       "yahoo api error",
			service:    "yahoo",
			statusCode: 503,
			url:        "https://fantasysports.yahooapis.com/...",
			wantCode:   ErrCodeExternalAPI,
		},
		{
			name:       "nba stats api error",
			service:    "nba_stats",
			statusCode: 500,
			url:        "https://stats.nba.com/...",
			wantCode:   ErrCodeExternalAPI,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewExternalAPIError(tt.service, "API error", tt.statusCode, tt.url)

			if err.Code() != tt.wantCode {
				t.Errorf("Code() = %v, want %v", err.Code(), tt.wantCode)
			}

			if err.Service() != tt.service {
				t.Errorf("Service = %v, want %v", err.Service(), tt.service)
			}

			if err.StatusCode() != tt.statusCode {
				t.Errorf("StatusCode = %v, want %v", err.StatusCode(), tt.statusCode)
			}

			if err.URL() != tt.url {
				t.Errorf("URL = %v, want %v", err.URL(), tt.url)
			}
		})
	}
}

func TestWrapExternalAPIError(t *testing.T) {
	originalErr := errors.New("connection reset")
	wrappedErr := WrapExternalAPIError(originalErr, "yahoo", "https://fantasysports.yahooapis.com/...", 503)

	if wrappedErr.Code() != ErrCodeExternalAPI {
		t.Errorf("Code() = %v, want %v", wrappedErr.Code(), ErrCodeExternalAPI)
	}
	if wrappedErr.Service() != "yahoo" {
		t.Errorf("Service = %v, want yahoo", wrappedErr.Service())
	}
	if wrappedErr.StatusCode() != 503 {
		t.Errorf("StatusCode = %v, want 503", wrappedErr.StatusCode())
	}
	if wrappedErr.URL() != "https://fantasysports.yahooapis.com/..." {
		t.Errorf("URL = %v, want the yahoo URL", wrappedErr.URL())
	}
	if !errors.Is(wrappedErr, originalErr) {
		t.Error("errors.Is() failed")
	}
}

func TestAuthenticationError(t *testing.T) {
	tests := []struct {
		name     string
		reason   string
		wantCode ErrorCode
	}{
		{
			name:     "token expired",
			reason:   "token_expired",
			wantCode: ErrCodeTokenExpired,
		},
		{
			name:     "token invalid",
			reason:   "token_invalid",
			wantCode: ErrCodeTokenInvalid,
		},
		{
			name:     "permission denied",
			reason:   "permission_denied",
			wantCode: ErrCodePermissionDenied,
		},
		{
			name:     "generic unauthorized",
			reason:   "other",
			wantCode: ErrCodeUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewAuthenticationError(tt.reason, "auth error")

			if err.Code() != tt.wantCode {
				t.Errorf("Code() = %v, want %v", err.Code(), tt.wantCode)
			}

			if err.Reason() != tt.reason {
				t.Errorf("Reason = %v, want %v", err.Reason(), tt.reason)
			}
		})
	}
}

func TestWrapAuthenticationError(t *testing.T) {
	tests := []struct {
		reason   string
		wantCode ErrorCode
	}{
		{"token_expired", ErrCodeTokenExpired},
		{"token_invalid", ErrCodeTokenInvalid},
		{"permission_denied", ErrCodePermissionDenied},
		{"other", ErrCodeUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			originalErr := errors.New("jwt: signature invalid")
			wrappedErr := WrapAuthenticationError(originalErr, tt.reason, "invalid authentication token")

			if wrappedErr.Code() != tt.wantCode {
				t.Errorf("Code() = %v, want %v", wrappedErr.Code(), tt.wantCode)
			}
			if wrappedErr.Reason() != tt.reason {
				t.Errorf("Reason = %v, want %v", wrappedErr.Reason(), tt.reason)
			}
			if !errors.Is(wrappedErr, originalErr) {
				t.Error("errors.Is() failed, wrapping not working")
			}

			// These codes are all a mayExposeOwnMessage category - the
			// explicit message argument is shown to the client despite
			// wrapping a cause, the same as NewAuthenticationError's.
			if got, want := UserMessage(wrappedErr), "invalid authentication token"; got != want {
				t.Errorf("UserMessage() = %q, want %q", got, want)
			}
		})
	}
}

func TestNotFoundError(t *testing.T) {
	err := NewNotFoundError("league", "12345")

	if err.Code() != ErrCodeNotFound {
		t.Errorf("Code() = %v, want %v", err.Code(), ErrCodeNotFound)
	}

	if err.ResourceType() != "league" {
		t.Errorf("ResourceType = %v, want league", err.ResourceType())
	}

	if err.ResourceID() != "12345" {
		t.Errorf("ResourceID = %v, want 12345", err.ResourceID())
	}

	expectedMsg := "league not found: 12345"
	if err.Error() != expectedMsg {
		t.Errorf("Error() = %v, want %v", err.Error(), expectedMsg)
	}
}

func TestWrapNotFoundError(t *testing.T) {
	originalErr := errors.New("sql: no rows in result set")
	wrappedErr := WrapNotFoundError(originalErr, "user", "42")

	if wrappedErr.Code() != ErrCodeNotFound {
		t.Errorf("Code() = %v, want %v", wrappedErr.Code(), ErrCodeNotFound)
	}
	if wrappedErr.ResourceType() != "user" {
		t.Errorf("ResourceType = %v, want user", wrappedErr.ResourceType())
	}
	if wrappedErr.ResourceID() != "42" {
		t.Errorf("ResourceID = %v, want 42", wrappedErr.ResourceID())
	}
	if !errors.Is(wrappedErr, originalErr) {
		t.Error("errors.Is() failed, wrapping not working")
	}

	// ErrCodeNotFound is a mayExposeOwnMessage category - the generated
	// message is shown to the client despite wrapping a cause, the same
	// as NewNotFoundError's, and never includes the wrapped cause's text.
	if got, want := UserMessage(wrappedErr), "user not found: 42"; got != want {
		t.Errorf("UserMessage() = %q, want %q", got, want)
	}
}

func TestConflictError(t *testing.T) {
	err := NewConflictError("team", "team_key", "team already exists")

	if err.Code() != ErrCodeAlreadyExists {
		t.Errorf("Code() = %v, want %v", err.Code(), ErrCodeAlreadyExists)
	}

	if err.ResourceType() != "team" {
		t.Errorf("ResourceType = %v, want team", err.ResourceType())
	}

	if err.ConflictKey() != "team_key" {
		t.Errorf("ConflictKey = %v, want team_key", err.ConflictKey())
	}
}

func TestWrapConflictError(t *testing.T) {
	originalErr := errors.New("unique constraint violation")
	wrappedErr := WrapConflictError(originalErr, "team", "team_key", "team already exists")

	if wrappedErr.Code() != ErrCodeAlreadyExists {
		t.Errorf("Code() = %v, want %v", wrappedErr.Code(), ErrCodeAlreadyExists)
	}
	if wrappedErr.ResourceType() != "team" {
		t.Errorf("ResourceType = %v, want team", wrappedErr.ResourceType())
	}
	if wrappedErr.ConflictKey() != "team_key" {
		t.Errorf("ConflictKey = %v, want team_key", wrappedErr.ConflictKey())
	}
	if !errors.Is(wrappedErr, originalErr) {
		t.Error("errors.Is() failed, wrapping not working")
	}
	if got, want := UserMessage(wrappedErr), "team already exists"; got != want {
		t.Errorf("UserMessage() = %q, want %q", got, want)
	}
}

func TestRateLimitError(t *testing.T) {
	err := NewRateLimitError("yahoo", 300, 60)

	if err.Code() != ErrCodeRateLimitExceeded {
		t.Errorf("Code() = %v, want %v", err.Code(), ErrCodeRateLimitExceeded)
	}

	if err.Service() != "yahoo" {
		t.Errorf("Service = %v, want yahoo", err.Service())
	}

	if err.Limit() != 300 {
		t.Errorf("Limit = %v, want 300", err.Limit())
	}

	if err.RetryAfter() != 60 {
		t.Errorf("RetryAfter = %v, want 60", err.RetryAfter())
	}
}

func TestRateLimitErrorClampsNegativeRetryAfter(t *testing.T) {
	err := NewRateLimitError("yahoo", 300, -1)

	if err.RetryAfter() != 0 {
		t.Errorf("RetryAfter = %v, want 0 (a negative value is not a valid Retry-After delay-seconds)", err.RetryAfter())
	}
	if err.Context()["retry_after"] != 0 {
		t.Errorf(`Context()["retry_after"] = %v, want 0, to stay consistent with the clamped RetryAfter field`, err.Context()["retry_after"])
	}
}

func TestWrapRateLimitError(t *testing.T) {
	originalErr := errors.New("redis: connection refused")
	wrappedErr := WrapRateLimitError(originalErr, "yahoo", 300, 60)

	if wrappedErr.Code() != ErrCodeRateLimitExceeded {
		t.Errorf("Code() = %v, want %v", wrappedErr.Code(), ErrCodeRateLimitExceeded)
	}
	if wrappedErr.Service() != "yahoo" {
		t.Errorf("Service = %v, want yahoo", wrappedErr.Service())
	}
	if wrappedErr.Limit() != 300 {
		t.Errorf("Limit = %v, want 300", wrappedErr.Limit())
	}
	if wrappedErr.RetryAfter() != 60 {
		t.Errorf("RetryAfter = %v, want 60", wrappedErr.RetryAfter())
	}
	if !errors.Is(wrappedErr, originalErr) {
		t.Error("errors.Is() failed, wrapping not working")
	}
	if got, want := UserMessage(wrappedErr), "rate limit exceeded for yahoo: 300 requests"; got != want {
		t.Errorf("UserMessage() = %q, want %q", got, want)
	}
}

func TestWrapRateLimitErrorClampsNegativeRetryAfter(t *testing.T) {
	wrappedErr := WrapRateLimitError(errors.New("redis: connection refused"), "yahoo", 300, -5)

	if wrappedErr.RetryAfter() != 0 {
		t.Errorf("RetryAfter = %v, want 0 (a negative value is not a valid Retry-After delay-seconds)", wrappedErr.RetryAfter())
	}
}

func TestInternalError(t *testing.T) {
	err := NewInternalError("optimizer", "unexpected error")

	if err.Code() != ErrCodeInternal {
		t.Errorf("Code() = %v, want %v", err.Code(), ErrCodeInternal)
	}

	if err.Component() != "optimizer" {
		t.Errorf("Component = %v, want optimizer", err.Component())
	}
}

func TestWrapInternalError(t *testing.T) {
	originalErr := errors.New("panic: nil pointer")
	wrappedErr := WrapInternalError(originalErr, "handler", "panic recovered")

	if wrappedErr.Code() != ErrCodeInternal {
		t.Errorf("Code() = %v, want %v", wrappedErr.Code(), ErrCodeInternal)
	}

	if wrappedErr.Component() != "handler" {
		t.Errorf("Component = %v, want handler", wrappedErr.Component())
	}

	if !errors.Is(wrappedErr, originalErr) {
		t.Error("errors.Is() failed")
	}
}

func TestNew(t *testing.T) {
	// New reaches codes with no dedicated constructor, e.g.
	// ErrCodeDatabaseConnection.
	err := New(ErrCodeDatabaseConnection, "could not reach the database")

	if err.Code() != ErrCodeDatabaseConnection {
		t.Errorf("Code() = %v, want %v", err.Code(), ErrCodeDatabaseConnection)
	}
	if err.Error() != "could not reach the database" {
		t.Errorf("Error() = %q, want %q", err.Error(), "could not reach the database")
	}
	if err.Unwrap() != nil {
		t.Errorf("Unwrap() = %v, want nil", err.Unwrap())
	}
	if len(err.StackTrace()) == 0 {
		t.Error("StackTrace() is empty")
	}

	// ErrCodeDatabaseConnection isn't in the client-input-shaped category
	// mayExposeOwnMessage trusts by default, so UserMessage falls back to
	// the generic per-code message regardless of whether there's a cause -
	// only SetPublicMessage can surface a database error's own text.
	if want := defaultMessageForCode(ErrCodeDatabaseConnection); UserMessage(err) != want {
		t.Errorf("UserMessage() = %q, want the generic default %q", UserMessage(err), want)
	}
}

func TestWrap(t *testing.T) {
	originalErr := errors.New("password=hunter2 host=10.0.0.1")
	err := Wrap(originalErr, ErrCodeDatabaseTransaction, "transaction failed")

	if err.Code() != ErrCodeDatabaseTransaction {
		t.Errorf("Code() = %v, want %v", err.Code(), ErrCodeDatabaseTransaction)
	}
	if !errors.Is(err, originalErr) {
		t.Error("errors.Is() failed to find the wrapped error")
	}

	// Wrap has a cause, so - same rule as the semantic Wrap* constructors -
	// its own Error() text (which embeds the cause) must not be surfaced to
	// clients by default.
	if got := UserMessage(err); got == err.Error() || strings.Contains(got, "hunter2") {
		t.Errorf("UserMessage() = %q, leaked the wrapped cause", got)
	}
}

func TestErrorTypeChecking(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		target     any
		shouldPass bool
	}{
		{
			name:       "is validation error",
			err:        NewValidationError("test", "field", nil),
			target:     new(*ValidationError),
			shouldPass: true,
		},
		{
			name:       "is not validation error",
			err:        NewDatabaseError("query", "test"),
			target:     new(*ValidationError),
			shouldPass: false,
		},
		{
			name:       "is database error",
			err:        NewDatabaseError("query", "test"),
			target:     new(*DatabaseError),
			shouldPass: true,
		},
		{
			name:       "is external api error",
			err:        NewExternalAPIError("yahoo", "test", 503, "url"),
			target:     new(*ExternalAPIError),
			shouldPass: true,
		},
		{
			name:       "is authentication error",
			err:        NewAuthenticationError("token_expired", "test"),
			target:     new(*AuthenticationError),
			shouldPass: true,
		},
		{
			name:       "is not found error",
			err:        NewNotFoundError("league", "123"),
			target:     new(*NotFoundError),
			shouldPass: true,
		},
		{
			name:       "is rate limit error",
			err:        NewRateLimitError("yahoo", 300, 60),
			target:     new(*RateLimitError),
			shouldPass: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := errors.As(tt.err, tt.target)
			if result != tt.shouldPass {
				t.Errorf("errors.As() = %v, want %v", result, tt.shouldPass)
			}
		})
	}
}

func TestGetErrorCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode ErrorCode
	}{
		{
			name:     "validation error code",
			err:      NewValidationError("test", "field", nil),
			wantCode: ErrCodeInvalidInput,
		},
		{
			name:     "database error code",
			err:      NewDatabaseError("query", "test"),
			wantCode: ErrCodeDatabaseQuery,
		},
		{
			name:     "not found error code",
			err:      NewNotFoundError("league", "123"),
			wantCode: ErrCodeNotFound,
		},
		{
			name:     "standard error defaults to internal",
			err:      errors.New("standard error"),
			wantCode: ErrCodeInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := GetErrorCode(tt.err)
			if code != tt.wantCode {
				t.Errorf("GetErrorCode() = %v, want %v", code, tt.wantCode)
			}
		})
	}
}

func TestGetStackTrace(t *testing.T) {
	err := NewInternalError("test", "test error")
	stack := GetStackTrace(err)

	if len(stack) == 0 {
		t.Error("GetStackTrace() returned empty slice")
	}

	// Stack should contain this test function
	found := false
	for _, frame := range stack {
		if strings.Contains(frame, "TestGetStackTrace") {
			found = true
			break
		}
	}

	if !found {
		t.Error("Stack trace does not contain current function")
	}
}

// minimalCodedError implements only Coder (Code() ErrorCode) plus the
// standard error interface - not Unwrap or StackTrace - to verify
// GetErrorCode/HTTPStatusCode work with the narrower Coder capability
// instead of requiring the full ErrorWithCode.
type minimalCodedError struct {
	code ErrorCode
	msg  string
}

func (e *minimalCodedError) Error() string   { return e.msg }
func (e *minimalCodedError) Code() ErrorCode { return e.code }

func TestGetErrorCodeWithMinimalCoderType(t *testing.T) {
	err := &minimalCodedError{code: ErrCodeNotFound, msg: "widget not found"}

	if got := GetErrorCode(err); got != ErrCodeNotFound {
		t.Errorf("GetErrorCode() = %v, want %v (a Coder-only type should be recognized)", got, ErrCodeNotFound)
	}
	if got := HTTPStatusCode(GetErrorCode(err)); got != http.StatusNotFound {
		t.Errorf("HTTPStatusCode(GetErrorCode(err)) = %d, want %d", got, http.StatusNotFound)
	}

	// No StackTrace() method - GetStackTrace must degrade gracefully
	// instead of requiring it too.
	if got := GetStackTrace(err); got != nil {
		t.Errorf("GetStackTrace() = %v, want nil for a type with no StackTrace method", got)
	}
}

// TestGetErrorCodeWithEmptyCoderCode guards a gap normalizeCode's use in New
// and Wrap didn't close: a third-party Coder-only type - a first-class,
// documented extension point, not an edge case - can return "" from Code()
// without going through either constructor. Before GetErrorCode normalized
// its result too, an empty code from a bare Coder rode straight through to
// the wire (e.g. HTTPErrorResponse.Error.Code == "").
func TestGetErrorCodeWithEmptyCoderCode(t *testing.T) {
	err := &minimalCodedError{code: "", msg: "empty code"}

	if got := GetErrorCode(err); got != ErrCodeInternal {
		t.Errorf("GetErrorCode() = %q, want %v (an empty external Coder code must normalize, not reach the wire)", got, ErrCodeInternal)
	}
}

// TestGetErrorCodeWithTypedNilCoder guards the classic Go footgun: a nil
// pointer assigned to an error variable produces a non-nil interface value
// (err == nil is false) whose concrete type still matches errors.As. Before
// outermostCoded filtered this case, GetErrorCode would call Code() on the
// nil receiver and panic instead of returning ErrCodeInternal.
func TestGetErrorCodeWithTypedNilCoder(t *testing.T) {
	var nilCoder *minimalCodedError
	var err error = nilCoder

	if got := GetErrorCode(err); got != ErrCodeInternal {
		t.Errorf("GetErrorCode() = %v, want %v (a typed-nil Coder must classify as internal, not dereference)", got, ErrCodeInternal)
	}

	var nilBaseErr *BaseError
	err = nilBaseErr
	if got := GetErrorCode(err); got != ErrCodeInternal {
		t.Errorf("GetErrorCode() = %v, want %v for a typed-nil *BaseError", got, ErrCodeInternal)
	}

	var nilNotFound *NotFoundError
	err = nilNotFound
	if got := GetErrorCode(err); got != ErrCodeInternal {
		t.Errorf("GetErrorCode() = %v, want %v for a typed-nil *NotFoundError", got, ErrCodeInternal)
	}
}

// TestGetStackTraceWithTypedNilCoder guards the same footgun
// TestGetErrorCodeWithTypedNilCoder does, at the sibling extraction point:
// before GetStackTrace filtered a typed-nil StackTracer, it called
// StackTrace() on the nil receiver and panicked instead of degrading to nil,
// the same way GetErrorCode used to panic on Code().
func TestGetStackTraceWithTypedNilCoder(t *testing.T) {
	var nilBaseErr *BaseError
	var err error = nilBaseErr
	if got := GetStackTrace(err); got != nil {
		t.Errorf("GetStackTrace() = %v, want nil for a typed-nil *BaseError", got)
	}

	var nilNotFound *NotFoundError
	err = nilNotFound
	if got := GetStackTrace(err); got != nil {
		t.Errorf("GetStackTrace() = %v, want nil for a typed-nil *NotFoundError", got)
	}
}

// TestRecaptureStackTraceWithTypedNilCoder guards the third extraction point
// sharing the same errors.As-then-dereference shape as GetErrorCode and
// GetStackTrace: before RecaptureStackTrace filtered a typed-nil
// stackTraceSetter, it called setStackTrace on the nil receiver and
// panicked instead of no-op'ing like it already does for an err with no
// setter at all.
func TestRecaptureStackTraceWithTypedNilCoder(t *testing.T) {
	var nilBaseErr *BaseError
	var err error = nilBaseErr

	RecaptureStackTrace(err, 0) // must not panic

	var nilNotFound *NotFoundError
	err = nilNotFound

	RecaptureStackTrace(err, 0) // must not panic
}

// nilSliceCoder is a non-pointer Coder: a named slice type. Coder is an
// open extension interface with no requirement that implementers be
// pointer-backed, so a nil value of this shape must classify the same
// safe way a typed-nil pointer does rather than panic when isNilValue
// fails to recognize it as nil.
type nilSliceCoder []string

func (e nilSliceCoder) Error() string   { return e[0] }
func (e nilSliceCoder) Code() ErrorCode { return ErrorCode(e[0]) }

// nilSliceStackTracer is the StackTracer analogue of nilSliceCoder.
type nilSliceStackTracer []string

func (e nilSliceStackTracer) Error() string        { return e[0] }
func (e nilSliceStackTracer) StackTrace() []string { return []string{e[0]} }

// TestGetErrorCodeWithNilNonPointerCoder guards the same footgun
// TestGetErrorCodeWithTypedNilCoder does, but for a Coder whose concrete
// type is a nil slice rather than a nil pointer. isNilValue used to check
// only reflect.Pointer, so this nil, non-pointer Coder reached Code() and
// panicked (indexing a nil slice) instead of classifying as internal.
func TestGetErrorCodeWithNilNonPointerCoder(t *testing.T) {
	var nilCoder nilSliceCoder
	var err error = nilCoder

	if got := GetErrorCode(err); got != ErrCodeInternal {
		t.Errorf("GetErrorCode() = %v, want %v (a nil non-pointer Coder must classify as internal, not dereference)", got, ErrCodeInternal)
	}
}

// TestGetStackTraceWithNilNonPointerCoder is the StackTracer counterpart of
// TestGetErrorCodeWithNilNonPointerCoder.
func TestGetStackTraceWithNilNonPointerCoder(t *testing.T) {
	var nilTracer nilSliceStackTracer
	var err error = nilTracer

	if got := GetStackTrace(err); got != nil {
		t.Errorf("GetStackTrace() = %v, want nil for a nil non-pointer StackTracer", got)
	}
}

// nilMapCoder, nilChanCoder, and nilFuncCoder round out nilSliceCoder's
// coverage of isNilValue's reflect.Kind switch with the other three
// reachable nil-capable kinds. reflect.Interface is in the switch too, to
// match reflect.Value.IsNil's documented kind set, but it cannot be
// exercised through GetErrorCode/GetStackTrace/RecaptureStackTrace: every
// call site converts an interface-typed local (Coder/StackTracer/
// stackTraceSetter) to `any` before calling isNilValue, and Go always
// flattens that conversion to the underlying concrete type - reflect.Kind()
// is never Interface for a value obtained this way, only for values reached
// through an unexported struct field accessed without Elem(). The
// reflect.Interface arm is defensive/unreachable dead code for this
// package's actual callers, not a gap a regression test can close.
type nilMapCoder map[string]ErrorCode

func (e nilMapCoder) Error() string { return "nil map coder" }
func (e nilMapCoder) Code() ErrorCode {
	e["code"] = ErrCodeInternal // panics: assignment to entry in nil map
	return e["code"]
}

type nilChanCoder chan string

func (e nilChanCoder) Error() string { return "nil chan coder" }
func (e nilChanCoder) Code() ErrorCode {
	close(e) // panics: close of nil channel
	return ErrCodeInternal
}

type nilFuncCoder func() ErrorCode

func (e nilFuncCoder) Error() string { return "nil func coder" }
func (e nilFuncCoder) Code() ErrorCode {
	return e() // panics: invalid memory address or nil pointer dereference
}

// TestGetErrorCodeWithNilMapCoder guards the map-kind arm of isNilValue's
// switch: a nil map assigned to Code()'s receiver would otherwise reach the
// method and panic on write (`e["code"] = ...`) instead of classifying as
// internal.
func TestGetErrorCodeWithNilMapCoder(t *testing.T) {
	var nilCoder nilMapCoder
	var err error = nilCoder

	if got := GetErrorCode(err); got != ErrCodeInternal {
		t.Errorf("GetErrorCode() = %v, want %v (a nil map Coder must classify as internal, not dereference)", got, ErrCodeInternal)
	}
}

// TestGetErrorCodeWithNilChanCoder is the chan-kind counterpart of
// TestGetErrorCodeWithNilMapCoder.
func TestGetErrorCodeWithNilChanCoder(t *testing.T) {
	var nilCoder nilChanCoder
	var err error = nilCoder

	if got := GetErrorCode(err); got != ErrCodeInternal {
		t.Errorf("GetErrorCode() = %v, want %v (a nil chan Coder must classify as internal, not dereference)", got, ErrCodeInternal)
	}
}

// TestGetErrorCodeWithNilFuncCoder is the func-kind counterpart of
// TestGetErrorCodeWithNilMapCoder.
func TestGetErrorCodeWithNilFuncCoder(t *testing.T) {
	var nilCoder nilFuncCoder
	var err error = nilCoder

	if got := GetErrorCode(err); got != ErrCodeInternal {
		t.Errorf("GetErrorCode() = %v, want %v (a nil func Coder must classify as internal, not dereference)", got, ErrCodeInternal)
	}
}

// structCoder is a value-type Coder whose kind (struct) is never nil-capable
// - isNilValue must fall through to its default case and treat it as not
// nil, the same way it treats a non-nil pointer, rather than misclassifying
// a perfectly usable value as absent.
type structCoder struct{ code ErrorCode }

func (e structCoder) Error() string   { return "struct coded error" }
func (e structCoder) Code() ErrorCode { return e.code }

// TestGetErrorCodeWithNonNilCapableValueCoder guards isNilValue's default
// case: a Coder concrete type (like a struct) that reflect can never report
// as nil must still classify normally instead of being mistaken for nil.
func TestGetErrorCodeWithNonNilCapableValueCoder(t *testing.T) {
	var err error = structCoder{code: ErrCodeNotFound}

	if got := GetErrorCode(err); got != ErrCodeNotFound {
		t.Errorf("GetErrorCode() = %v, want %v (a non-nil-capable value Coder must classify normally)", got, ErrCodeNotFound)
	}
}

// minimalCodedUnwrappableError implements Coder, error, and Unwrap, but not
// StackTracer - to verify getUserFriendlyMessage/UserMessage's safety
// property (never surface a wrapped cause's text without an explicit
// override) doesn't require the full ErrorWithCode either.
type minimalCodedUnwrappableError struct {
	code  ErrorCode
	msg   string
	cause error
}

func (e *minimalCodedUnwrappableError) Error() string {
	if e.cause != nil {
		return e.msg + ": " + e.cause.Error()
	}
	return e.msg
}
func (e *minimalCodedUnwrappableError) Code() ErrorCode { return e.code }
func (e *minimalCodedUnwrappableError) Unwrap() error   { return e.cause }

func TestUserMessageWithMinimalCoderUnwrapperType(t *testing.T) {
	secret := errors.New("password=hunter2")
	wrapped := &minimalCodedUnwrappableError{code: ErrCodeDatabaseQuery, msg: "query failed", cause: secret}

	got := UserMessage(wrapped)
	if strings.Contains(got, "hunter2") {
		t.Errorf("UserMessage() = %q, leaked the wrapped cause even though this type isn't the full ErrorWithCode", got)
	}
	if want := defaultMessageForCode(ErrCodeDatabaseQuery); got != want {
		t.Errorf("UserMessage() = %q, want the generic default %q", got, want)
	}

	// No cause - its own text is safe to surface, same rule as Wrap*/New.
	plain := &minimalCodedUnwrappableError{code: ErrCodeNotFound, msg: "widget 42 not found"}
	if got := UserMessage(plain); got != "widget 42 not found" {
		t.Errorf("UserMessage() = %q, want the error's own text (no cause)", got)
	}
}

func TestErrorWrapping(t *testing.T) {
	originalErr := errors.New("original error")
	wrappedErr := WrapDatabaseError(originalErr, "query", "SELECT...")

	// Test errors.Is
	if !errors.Is(wrappedErr, originalErr) {
		t.Error("errors.Is() failed to find original error")
	}

	// Test errors.As
	var dbErr *DatabaseError
	if !errors.As(wrappedErr, &dbErr) {
		t.Error("errors.As() failed to extract DatabaseError")
	}

	if dbErr.Operation() != "query" {
		t.Errorf("DatabaseError.Operation() = %v, want query", dbErr.Operation())
	}
}

func TestPublicMessage(t *testing.T) {
	err := NewDatabaseError("query", "connection to 10.0.4.12:5432 refused")

	if msg, ok := err.PublicMessage(); ok || msg != "" {
		t.Errorf("PublicMessage() = (%q, %v), want (\"\", false) before SetPublicMessage", msg, ok)
	}

	err.SetPublicMessage("We're having trouble reaching the database. Please try again shortly.")

	msg, ok := err.PublicMessage()
	if !ok {
		t.Fatal("PublicMessage() ok = false after SetPublicMessage")
	}
	if msg != "We're having trouble reaching the database. Please try again shortly." {
		t.Errorf("PublicMessage() = %q, unexpected", msg)
	}

	// getUserFriendlyMessage (and so UserMessage/WriteHTTPError) must
	// prefer the override over err.Error(), even through a wrap.
	if got := getUserFriendlyMessage(err.Code(), err); got != msg {
		t.Errorf("getUserFriendlyMessage() = %q, want the public message override %q", got, msg)
	}
	if got := UserMessage(err); got != msg {
		t.Errorf("UserMessage() = %q, want the public message override %q", got, msg)
	}

	wrapped := fmt.Errorf("service layer: %w", err)
	if got := UserMessage(wrapped); got != msg {
		t.Errorf("UserMessage(wrapped) = %q, want the public message override %q (should unwrap via errors.As)", got, msg)
	}
}

func TestMayExposeOwnMessageIsCategoryBased(t *testing.T) {
	// WrapValidationError always sets a cause, but its message is still an
	// explicit caller argument (never derived from the cause) - under the
	// category-based policy it's shown, unlike the old cause-presence rule
	// which hid every Wrap* message regardless of code.
	secret := errors.New("regexp compile failed: (?P<bad")
	wrapped := WrapValidationError(secret, "email must be a valid address", "email")
	if got, want := UserMessage(wrapped), "email must be a valid address"; got != want {
		t.Errorf("UserMessage() = %q, want %q", got, want)
	}
	if strings.Contains(UserMessage(wrapped), "regexp") {
		t.Errorf("UserMessage() leaked the wrapped cause: %q", UserMessage(wrapped))
	}

	// NewInternalError has no cause, but ErrCodeInternal isn't in the
	// trusted category - its own message must not be surfaced automatically.
	internal := NewInternalError("billing", "stripe secret sk_live_do_not_leak rejected")
	if got := UserMessage(internal); strings.Contains(got, "sk_live") {
		t.Errorf("UserMessage() = %q, leaked NewInternalError's own message despite no SetPublicMessage override", got)
	}
	if got, want := UserMessage(internal), defaultMessageForCode(ErrCodeInternal); got != want {
		t.Errorf("UserMessage() = %q, want the generic default %q", got, want)
	}

	// An explicit SetPublicMessage override still wins regardless of category.
	internal.SetPublicMessage("We're investigating a billing issue.")
	if got, want := UserMessage(internal), "We're investigating a billing issue."; got != want {
		t.Errorf("UserMessage() = %q, want the SetPublicMessage override %q", got, want)
	}
}

// minimalCoderWrapper implements only Coder, error, and Unwrap - not
// PublicMessager - to verify getUserFriendlyMessage resolves
// SetPublicMessage from the same outermost coded node the code came from,
// not from anywhere in the chain.
type minimalCoderWrapper struct {
	err error
}

func (e *minimalCoderWrapper) Error() string   { return "internal failure" }
func (e *minimalCoderWrapper) Unwrap() error   { return e.err }
func (e *minimalCoderWrapper) Code() ErrorCode { return ErrCodeInternal }

func TestPublicMessageDoesNotCrossOuterClassification(t *testing.T) {
	inner := NewNotFoundError("user", "secret@example.com")
	inner.SetPublicMessage("account secret@example.com was not found")

	outer := &minimalCoderWrapper{err: inner}

	if got, want := GetErrorCode(outer), ErrCodeInternal; got != want {
		t.Fatalf("GetErrorCode() = %v, want %v", got, want)
	}

	// outer doesn't implement PublicMessager, and ErrCodeInternal isn't in
	// the mayExposeOwnMessage category, so this must fall back to the
	// generic default - never the inner NotFoundError's override, which
	// belongs to a different classification (NOT_FOUND) than the one this
	// response is actually reporting (INTERNAL_ERROR).
	if got, want := UserMessage(outer), defaultMessageForCode(ErrCodeInternal); got != want {
		t.Errorf("UserMessage() = %q, want the generic default %q (inner SetPublicMessage override must not leak through outer's different classification)", got, want)
	}
}

func TestPublicDetailAdditionsAndRemovals(t *testing.T) {
	t.Run("New/Wrap have no automatic details, but SetPublicDetail adds them", func(t *testing.T) {
		err := New(ErrCodeConstraintViolation, "out of stock")
		err.SetPublicDetail("sku", "WIDGET-42")
		err.SetPublicDetail("available", 0)

		got := extractErrorDetails(err)
		if got["sku"] != "WIDGET-42" {
			t.Errorf(`details["sku"] = %v, want "WIDGET-42"`, got["sku"])
		}
		if got["available"] != 0 {
			t.Errorf(`details["available"] = %v, want 0`, got["available"])
		}
	})

	t.Run("SetPublicDetail overrides a built-in type's automatic key", func(t *testing.T) {
		err := NewNotFoundError("user", "secret@example.com")
		err.SetPublicDetail("resource_id", "[redacted]")

		got := extractErrorDetails(err)
		if got["resource_id"] != "[redacted]" {
			t.Errorf(`details["resource_id"] = %v, want "[redacted]"`, got["resource_id"])
		}
		if got["resource_type"] != "user" {
			t.Errorf(`details["resource_type"] = %v, want "user" (unrelated built-in keys stay)`, got["resource_type"])
		}
	})

	t.Run("RemovePublicDetail suppresses a built-in type's automatic key", func(t *testing.T) {
		err := NewNotFoundError("user", "secret@example.com")
		err.RemovePublicDetail("resource_id")

		got := extractErrorDetails(err)
		if _, present := got["resource_id"]; present {
			t.Errorf(`details["resource_id"] = %v, want the key entirely absent`, got["resource_id"])
		}
		if got["resource_type"] != "user" {
			t.Errorf(`details["resource_type"] = %v, want "user" (unrelated built-in keys stay)`, got["resource_type"])
		}
	})

	t.Run("removing every key leaves details nil, not an empty map", func(t *testing.T) {
		err := NewValidationError("bad email", "email", "not-an-email")
		err.RemovePublicDetail("field")

		if got := extractErrorDetails(err); got != nil {
			t.Errorf("extractErrorDetails() = %v, want nil", got)
		}
	})

	t.Run("SetPublicDetail after RemovePublicDetail un-suppresses the key", func(t *testing.T) {
		err := NewNotFoundError("user", "secret@example.com")
		err.RemovePublicDetail("resource_id")
		err.SetPublicDetail("resource_id", "safe-id-123")

		got := extractErrorDetails(err)
		if got["resource_id"] != "safe-id-123" {
			t.Errorf(`details["resource_id"] = %v, want "safe-id-123" (the later SetPublicDetail should win)`, got["resource_id"])
		}
	})

	t.Run("RemovePublicDetail after SetPublicDetail re-suppresses the key", func(t *testing.T) {
		err := New(ErrCodeConstraintViolation, "out of stock")
		err.SetPublicDetail("sku", "WIDGET-42")
		err.RemovePublicDetail("sku")

		got := extractErrorDetails(err)
		if _, present := got["sku"]; present {
			t.Errorf(`details["sku"] = %v, want the key entirely absent (the later RemovePublicDetail should win)`, got["sku"])
		}
	})

	t.Run("RemovePublicDetail alone does not redact the identifier from message", func(t *testing.T) {
		// NewNotFoundError's own message embeds resourceID directly, and
		// NOT_FOUND is a category mayExposeOwnMessage shows by default -
		// RemovePublicDetail only touches the details map. This documents
		// the gap the README warns about, not a desired outcome.
		err := NewNotFoundError("user", "secret@example.com")
		err.RemovePublicDetail("resource_id")

		if got := UserMessage(err); !strings.Contains(got, "secret@example.com") {
			t.Errorf("UserMessage() = %q, want it to still contain the identifier (documenting that RemovePublicDetail alone doesn't redact message)", got)
		}
	})

	t.Run("RemovePublicDetail plus SetPublicMessage fully redacts the identifier", func(t *testing.T) {
		err := NewNotFoundError("user", "secret@example.com")
		err.RemovePublicDetail("resource_id")
		err.SetPublicMessage("user was not found")

		if got := UserMessage(err); strings.Contains(got, "secret@example.com") {
			t.Errorf("UserMessage() = %q, leaked the identifier despite SetPublicMessage", got)
		}
		if got := extractErrorDetails(err); got["resource_id"] != nil {
			t.Errorf(`details["resource_id"] = %v, want absent`, got["resource_id"])
		}
	})
}

func TestProblemTypeAndInstanceOverrides(t *testing.T) {
	err := NewNotFoundError("league", "42")

	if _, set := err.ProblemType(); set {
		t.Error("ProblemType() set = true before SetProblemType, want false")
	}
	err.SetProblemType("https://example.com/problems/resource-not-found")
	if got, ok := err.ProblemType(); !ok || got != "https://example.com/problems/resource-not-found" {
		t.Errorf("ProblemType() = (%q, %v), want the URI set by SetProblemType", got, ok)
	}

	if _, set := err.ProblemInstance(); set {
		t.Error("ProblemInstance() set = true before SetProblemInstance, want false")
	}
	err.SetProblemInstance("https://example.com/requests/abc123")
	if got, ok := err.ProblemInstance(); !ok || got != "https://example.com/requests/abc123" {
		t.Errorf("ProblemInstance() = (%q, %v), want the URI set by SetProblemInstance", got, ok)
	}

	if _, set := err.ProblemTitle(); set {
		t.Error("ProblemTitle() set = true before SetProblemTitle, want false")
	}
	err.SetProblemTitle("League not found")
	if got, ok := err.ProblemTitle(); !ok || got != "League not found" {
		t.Errorf("ProblemTitle() = (%q, %v), want the title set by SetProblemTitle", got, ok)
	}
}

func TestDatabaseErrorCodeFromOperation(t *testing.T) {
	tests := []struct {
		operation string
		want      ErrorCode
	}{
		{"query", ErrCodeDatabaseQuery},
		{"insert", ErrCodeDatabaseQuery},
		{"update", ErrCodeDatabaseQuery},
		{"delete", ErrCodeDatabaseQuery},
		{"transaction", ErrCodeDatabaseTransaction},
		{"migration", ErrCodeDatabaseMigration},
		{"", ErrCodeDatabaseQuery},
	}
	for _, tt := range tests {
		t.Run(tt.operation, func(t *testing.T) {
			if got := NewDatabaseError(tt.operation, "boom").Code(); got != tt.want {
				t.Errorf("NewDatabaseError(%q, ...).Code() = %v, want %v", tt.operation, got, tt.want)
			}
			if got := WrapDatabaseError(errors.New("cause"), tt.operation, "SELECT 1").Code(); got != tt.want {
				t.Errorf("WrapDatabaseError(_, %q, ...).Code() = %v, want %v", tt.operation, got, tt.want)
			}
		})
	}

	// Both still map to the same HTTP status as ErrCodeDatabaseQuery.
	if HTTPStatusCode(ErrCodeDatabaseTransaction) != HTTPStatusCode(ErrCodeDatabaseQuery) {
		t.Error("ErrCodeDatabaseTransaction should map to the same status as ErrCodeDatabaseQuery")
	}
	if HTTPStatusCode(ErrCodeDatabaseMigration) != HTTPStatusCode(ErrCodeDatabaseQuery) {
		t.Error("ErrCodeDatabaseMigration should map to the same status as ErrCodeDatabaseQuery")
	}
}

func TestRecaptureStackTrace(t *testing.T) {
	// newViaHelper mimics a caller wrapping a constructor in their own
	// helper function - without RecaptureStackTrace, the trace's top
	// frame would be this helper, not TestRecaptureStackTrace.
	newViaHelper := func() *InternalError {
		err := NewInternalError("test", "boom")
		RecaptureStackTrace(err, 1)
		return err
	}

	err := newViaHelper()
	stack := err.StackTrace()
	if len(stack) == 0 {
		t.Fatal("StackTrace() is empty")
	}
	if !strings.Contains(stack[0], "TestRecaptureStackTrace") {
		t.Errorf("stack[0] = %q, want it to reference TestRecaptureStackTrace (the helper's caller), not the helper", stack[0])
	}

	// A non-svcerr error is left untouched rather than panicking.
	plain := errors.New("plain")
	RecaptureStackTrace(plain, 1)
}

func TestErrorContext(t *testing.T) {
	err := NewValidationError("test error", "email", "invalid@")

	ctx := err.Context()
	if ctx == nil {
		t.Fatal("Context() returned nil")
	}

	if field, ok := ctx["field"]; !ok || field != "email" {
		t.Errorf("Context[field] = %v, want email", field)
	}

	if value, ok := ctx["value"]; !ok || value != "invalid@" {
		t.Errorf("Context[value] = %v, want invalid@", value)
	}
}

func TestErrorContextReturnsCopy(t *testing.T) {
	err := NewValidationError("test error", "email", "invalid@")

	ctx := err.Context()
	ctx["field"] = "tampered"
	ctx["new_key"] = "injected"

	fresh := err.Context()
	if fresh["field"] != "email" {
		t.Errorf("Context()[field] = %v after mutating a previous copy, want email (internal state was mutated)", fresh["field"])
	}
	if _, ok := fresh["new_key"]; ok {
		t.Error("Context() reflects a key added to a previously returned copy (internal state was mutated)")
	}
}

func TestStackTraceReturnsCopy(t *testing.T) {
	err := NewInternalError("test", "test error")

	stack := err.StackTrace()
	if len(stack) == 0 {
		t.Fatal("StackTrace() is empty")
	}
	original := stack[0]
	stack[0] = "tampered"

	fresh := err.StackTrace()
	if fresh[0] != original {
		t.Errorf("StackTrace()[0] = %q after mutating a previous copy, want %q (internal state was mutated)", fresh[0], original)
	}
}

func TestStackTraceFiltering(t *testing.T) {
	err := NewInternalError("test", "test error")
	stack := err.StackTrace()

	// Stack should be filtered to show only relevant paths
	for _, frame := range stack {
		// Should not contain full absolute paths
		if strings.HasPrefix(frame, "/") {
			t.Errorf("Stack frame contains unfiltered absolute path: %s", frame)
		}
	}
}

func TestFormatStackTraceEmpty(t *testing.T) {
	if got := formatStackTrace(nil); got != nil {
		t.Errorf("formatStackTrace(nil) = %v, want nil", got)
	}
	if got := formatStackTrace([]uintptr{}); got != nil {
		t.Errorf("formatStackTrace([]uintptr{}) = %v, want nil", got)
	}
}

func TestMultipleErrorWrapping(t *testing.T) {
	// Create chain: original -> wrapped in validation -> wrapped in internal
	originalErr := errors.New("parse error")
	validationErr := WrapValidationError(originalErr, "validation failed", "input")
	internalErr := WrapInternalError(validationErr, "handler", "processing failed")

	// Should be able to unwrap to find original
	if !errors.Is(internalErr, originalErr) {
		t.Error("Multi-level wrapping failed, cannot find original error")
	}

	// Should be able to extract any error in chain
	var ve *ValidationError
	if !errors.As(internalErr, &ve) {
		t.Error("Cannot extract ValidationError from chain")
	}

	var ie *InternalError
	if !errors.As(internalErr, &ie) {
		t.Error("Cannot extract InternalError from chain")
	}
}

func TestHTTPHelpersUnwrapWrappedErrors(t *testing.T) {
	// getUserFriendlyMessage, extractErrorDetails, and logError use
	// errors.As rather than a raw type assertion, so they must still
	// find type-specific details when the error is wrapped (e.g. by a
	// caller doing fmt.Errorf("...: %w", err)) instead of passed as-is.
	inner := NewValidationError("invalid email", "email", "not-an-email")
	wrapped := fmt.Errorf("request failed: %w", inner)

	msg := getUserFriendlyMessage(GetErrorCode(wrapped), wrapped)
	if msg != inner.Error() {
		t.Errorf("getUserFriendlyMessage() = %q, want %q", msg, inner.Error())
	}

	details := extractErrorDetails(wrapped)
	if details["field"] != "email" {
		t.Errorf("extractErrorDetails()[\"field\"] = %v, want email", details["field"])
	}

	var loggedFields map[string]any
	logError(loggerFunc(func(_ Level, _ error, fields map[string]any, _ string) {
		loggedFields = fields
	}), wrapped, http.StatusBadRequest, nil, nil, 0)

	if loggedFields["field"] != "email" {
		t.Errorf("logError() fields[\"field\"] = %v, want email", loggedFields["field"])
	}
}

func TestUserMessage(t *testing.T) {
	inner := NewNotFoundError("league", "12345")
	wrapped := fmt.Errorf("lookup failed: %w", inner)

	if got, want := UserMessage(inner), inner.Error(); got != want {
		t.Errorf("UserMessage(inner) = %q, want %q", got, want)
	}
	if got, want := UserMessage(wrapped), inner.Error(); got != want {
		t.Errorf("UserMessage(wrapped) = %q, want %q", got, want)
	}

	plain := errors.New("boom")
	if got, want := UserMessage(plain), "An internal error occurred. Please contact support if the problem persists."; got != want {
		t.Errorf("UserMessage(plain) = %q, want %q", got, want)
	}
}

// loggerFunc adapts a plain function to the Logger interface for tests.
type loggerFunc func(level Level, err error, fields map[string]any, msg string)

func (f loggerFunc) Log(level Level, err error, fields map[string]any, msg string) {
	f(level, err, fields, msg)
}

// TestPublicDetailsReturnsCopies covers assessment v0.6.4/M2:
// PublicDetails used to return the error's internal addition/removal maps
// by reference, so a caller could mutate error state - injecting keys into
// the next rendered response - without going through SetPublicDetail/
// RemovePublicDetail, bypassing their last-call-wins bookkeeping and the
// documented mutation surface. The maps must be shallow copies, matching
// Context()'s contract.
func TestPublicDetailsReturnsCopies(t *testing.T) {
	t.Run("mutating the returned maps does not reach the error", func(t *testing.T) {
		err := NewNotFoundError("widget", "42")
		err.SetPublicDetail("field", "email")
		err.RemovePublicDetail("resource_id")

		add, remove := err.PublicDetails()
		add["secret"] = "unexpectedly public"
		delete(add, "field")
		remove["resource_type"] = struct{}{}
		delete(remove, "resource_id")

		addAfter, removeAfter := err.PublicDetails()
		if _, ok := addAfter["secret"]; ok {
			t.Error(`addAfter["secret"] present - mutating the returned addition map reached the error's internal state`)
		}
		if addAfter["field"] != "email" {
			t.Errorf(`addAfter["field"] = %v, want "email" - deleting from the returned map must not reach the error`, addAfter["field"])
		}
		if _, ok := removeAfter["resource_type"]; ok {
			t.Error(`removeAfter["resource_type"] present - mutating the returned removal map reached the error's internal state`)
		}
		if _, ok := removeAfter["resource_id"]; !ok {
			t.Error(`removeAfter["resource_id"] missing - deleting from the returned map must not reach the error`)
		}
	})

	t.Run("nothing set returns nil maps", func(t *testing.T) {
		err := NewNotFoundError("widget", "42")
		add, remove := err.PublicDetails()
		if add != nil || remove != nil {
			t.Errorf("PublicDetails() = %v, %v, want nil, nil when nothing was set", add, remove)
		}
	})
}

// TestJoinedErrorClassificationIsChildOrderDependent pins the documented
// (assessment v0.6.4/L1) errors.Join semantics: classification follows
// errors.As pre-order, depth-first traversal, so the first coded child
// wins and reversing errors.Join's arguments changes the response. Not a
// recommendation - the package doc tells callers aggregating mixed
// severities to classify the aggregate explicitly via Wrap - but the
// traversal order is stdlib behavior this package inherits, and a change
// in it should be caught, not discovered in production.
func TestJoinedErrorClassificationIsChildOrderDependent(t *testing.T) {
	notFound := NewNotFoundError("user", "123")
	internal := NewInternalError("repository", "database failed")

	if code := GetErrorCode(errors.Join(notFound, internal)); code != ErrCodeNotFound {
		t.Errorf("GetErrorCode(Join(notFound, internal)) = %v, want %v (first coded child wins)", code, ErrCodeNotFound)
	}
	if code := GetErrorCode(errors.Join(internal, notFound)); code != ErrCodeInternal {
		t.Errorf("GetErrorCode(Join(internal, notFound)) = %v, want %v (first coded child wins)", code, ErrCodeInternal)
	}

	// The documented idiom: explicit classification of the aggregate makes
	// the child order irrelevant.
	joined := errors.Join(notFound, internal)
	wrapped := Wrap(joined, ErrCodeInternal, "request processing failed")
	if code := GetErrorCode(wrapped); code != ErrCodeInternal {
		t.Errorf("GetErrorCode(Wrap(joined, ErrCodeInternal, ...)) = %v, want %v", code, ErrCodeInternal)
	}
	if !errors.Is(wrapped, notFound) || !errors.Is(wrapped, internal) {
		t.Error("explicit classification must preserve the joined children for errors.Is")
	}
}

// TestExternalAPIErrorSetRetryAfter covers the only way to attach an
// upstream retry hint: the setter clamps on the way in, and since v1 the
// field is unexported, so a valid non-negative stored value is a real
// invariant the emission paths trust without re-clamping.
func TestExternalAPIErrorSetRetryAfter(t *testing.T) {
	err := NewExternalAPIError("upstream", "upstream 503", 503, "https://api.example.com")
	if _, ok := err.RetryAfter(); ok {
		t.Fatal("RetryAfter() reported a hint before any was recorded")
	}

	err.SetRetryAfter(30)
	if got, ok := err.RetryAfter(); !ok || got != 30 {
		t.Fatalf("RetryAfter() = %d, %v, want 30, true", got, ok)
	}

	err.SetRetryAfter(-9)
	if got, ok := err.RetryAfter(); !ok || got != 0 {
		t.Errorf("RetryAfter() = %d, %v, want 0, true - the setter clamps negative hints at the source", got, ok)
	}
}

// TestContextDerivation pins the v1 Context() model: derived on demand
// from each type's canonical identity fields (never snapshotted), so it
// can never disagree with what the response writers and log fields
// report. Includes the normalizations v1 introduced against the old
// construction-time snapshots, documented in docs/v1-design-pass.md's
// stage-3 amendment.
func TestContextDerivation(t *testing.T) {
	withHint := NewExternalAPIError("yahoo", "call failed", 503, "https://api.example.com")
	withHint.SetRetryAfter(30)

	cases := []struct {
		name string
		err  interface{ Context() map[string]any }
		want map[string]any
	}{
		{"BaseError via New", New(ErrCodeQuotaExceeded, "quota"), nil},
		{"BaseError via Wrap", Wrap(errors.New("cause"), ErrCodeQuotaExceeded, "quota"), nil},
		{"ValidationError", NewValidationError("bad", "email", "x@"),
			map[string]any{"field": "email", "value": "x@"}},
		{"WrapValidationError includes nil value", WrapValidationError(errors.New("cause"), "bad", "email"),
			map[string]any{"field": "email", "value": nil}},
		{"NewDatabaseError omits empty query", NewDatabaseError("insert", "dup"),
			map[string]any{"operation": "insert"}},
		{"WrapDatabaseError includes query", WrapDatabaseError(errors.New("cause"), "query", "SELECT 1"),
			map[string]any{"operation": "query", "query": "SELECT 1"}},
		{"ExternalAPIError without hint", NewExternalAPIError("yahoo", "call failed", 503, "https://api.example.com"),
			map[string]any{"service": "yahoo", "status_code": 503, "url": "https://api.example.com"}},
		{"ExternalAPIError with hint includes retry_after", withHint,
			map[string]any{"service": "yahoo", "status_code": 503, "url": "https://api.example.com", "retry_after": 30}},
		{"AuthenticationError", NewAuthenticationError("token_expired", "expired"),
			map[string]any{"reason": "token_expired"}},
		{"NotFoundError", NewNotFoundError("league", "12345"),
			map[string]any{"resource_type": "league", "resource_id": "12345"}},
		{"ConflictError", NewConflictError("user", "email", "dup"),
			map[string]any{"resource_type": "user", "conflict_key": "email"}},
		{"RateLimitError", NewRateLimitError("api", 100, 30),
			map[string]any{"service": "api", "limit": 100, "retry_after": 30}},
		{"InternalError", NewInternalError("billing", "boom"),
			map[string]any{"component": "billing"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.err.Context()
			if len(got) != len(tc.want) {
				t.Fatalf("Context() = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("Context()[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}

	t.Run("each call builds a fresh map", func(t *testing.T) {
		err := NewNotFoundError("league", "12345")
		first := err.Context()
		first["resource_id"] = "tampered"
		delete(first, "resource_type")
		second := err.Context()
		if second["resource_id"] != "12345" || second["resource_type"] != "league" {
			t.Errorf("second Context() = %v - mutating an earlier result must not reach the error", second)
		}
	})
}

// TestIdentityAccessors exercises every v1 accessor against its
// construction inputs - the read side of the compiler-enforced
// immutable-identity contract.
func TestIdentityAccessors(t *testing.T) {
	v := NewValidationError("bad", "email", "x@")
	if v.Field() != "email" || v.Value() != "x@" {
		t.Errorf("ValidationError accessors = %q/%v, want email/x@", v.Field(), v.Value())
	}
	d := WrapDatabaseError(errors.New("x"), "insert", "INSERT ...")
	if d.Operation() != "insert" || d.Query() != "INSERT ..." {
		t.Errorf("DatabaseError accessors = %q/%q", d.Operation(), d.Query())
	}
	e := NewExternalAPIError("yahoo", "failed", 503, "https://u")
	if e.Service() != "yahoo" || e.StatusCode() != 503 || e.URL() != "https://u" {
		t.Errorf("ExternalAPIError accessors = %q/%d/%q", e.Service(), e.StatusCode(), e.URL())
	}
	a := NewAuthenticationError("token_expired", "expired")
	if a.Reason() != "token_expired" {
		t.Errorf("Reason() = %q", a.Reason())
	}
	n := NewNotFoundError("league", "12345")
	if n.ResourceType() != "league" || n.ResourceID() != "12345" {
		t.Errorf("NotFoundError accessors = %q/%q", n.ResourceType(), n.ResourceID())
	}
	c := NewConflictError("user", "email", "dup")
	if c.ResourceType() != "user" || c.ConflictKey() != "email" {
		t.Errorf("ConflictError accessors = %q/%q", c.ResourceType(), c.ConflictKey())
	}
	r := NewRateLimitError("api", 100, 30)
	if r.Service() != "api" || r.Limit() != 100 || r.RetryAfter() != 30 {
		t.Errorf("RateLimitError accessors = %q/%d/%d", r.Service(), r.Limit(), r.RetryAfter())
	}
	i := NewInternalError("billing", "boom")
	if i.Component() != "billing" {
		t.Errorf("Component() = %q", i.Component())
	}
}
