package errors

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
		value     interface{}
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

			if err.Field != tt.wantField {
				t.Errorf("Field = %v, want %v", err.Field, tt.wantField)
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

	if wrappedErr.Field != "user_id" {
		t.Errorf("Field = %v, want user_id", wrappedErr.Field)
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

			if err.Operation != tt.operation {
				t.Errorf("Operation = %v, want %v", err.Operation, tt.operation)
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

	if wrappedErr.Operation != "query" {
		t.Errorf("Operation = %v, want query", wrappedErr.Operation)
	}

	if wrappedErr.Query != "SELECT * FROM leagues" {
		t.Errorf("Query = %v, want SELECT * FROM leagues", wrappedErr.Query)
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

			if err.Service != tt.service {
				t.Errorf("Service = %v, want %v", err.Service, tt.service)
			}

			if err.StatusCode != tt.statusCode {
				t.Errorf("StatusCode = %v, want %v", err.StatusCode, tt.statusCode)
			}

			if err.URL != tt.url {
				t.Errorf("URL = %v, want %v", err.URL, tt.url)
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
	if wrappedErr.Service != "yahoo" {
		t.Errorf("Service = %v, want yahoo", wrappedErr.Service)
	}
	if wrappedErr.StatusCode != 503 {
		t.Errorf("StatusCode = %v, want 503", wrappedErr.StatusCode)
	}
	if wrappedErr.URL != "https://fantasysports.yahooapis.com/..." {
		t.Errorf("URL = %v, want the yahoo URL", wrappedErr.URL)
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

			if err.Reason != tt.reason {
				t.Errorf("Reason = %v, want %v", err.Reason, tt.reason)
			}
		})
	}
}

func TestNotFoundError(t *testing.T) {
	err := NewNotFoundError("league", "12345")

	if err.Code() != ErrCodeNotFound {
		t.Errorf("Code() = %v, want %v", err.Code(), ErrCodeNotFound)
	}

	if err.ResourceType != "league" {
		t.Errorf("ResourceType = %v, want league", err.ResourceType)
	}

	if err.ResourceID != "12345" {
		t.Errorf("ResourceID = %v, want 12345", err.ResourceID)
	}

	expectedMsg := "league not found: 12345"
	if err.Error() != expectedMsg {
		t.Errorf("Error() = %v, want %v", err.Error(), expectedMsg)
	}
}

func TestConflictError(t *testing.T) {
	err := NewConflictError("team", "team_key", "team already exists")

	if err.Code() != ErrCodeAlreadyExists {
		t.Errorf("Code() = %v, want %v", err.Code(), ErrCodeAlreadyExists)
	}

	if err.ResourceType != "team" {
		t.Errorf("ResourceType = %v, want team", err.ResourceType)
	}

	if err.ConflictKey != "team_key" {
		t.Errorf("ConflictKey = %v, want team_key", err.ConflictKey)
	}
}

func TestRateLimitError(t *testing.T) {
	err := NewRateLimitError("yahoo", 300, 60)

	if err.Code() != ErrCodeRateLimitExceeded {
		t.Errorf("Code() = %v, want %v", err.Code(), ErrCodeRateLimitExceeded)
	}

	if err.Service != "yahoo" {
		t.Errorf("Service = %v, want yahoo", err.Service)
	}

	if err.Limit != 300 {
		t.Errorf("Limit = %v, want 300", err.Limit)
	}

	if err.RetryAfter != 60 {
		t.Errorf("RetryAfter = %v, want 60", err.RetryAfter)
	}
}

func TestInternalError(t *testing.T) {
	err := NewInternalError("optimizer", "unexpected error")

	if err.Code() != ErrCodeInternal {
		t.Errorf("Code() = %v, want %v", err.Code(), ErrCodeInternal)
	}

	if err.Component != "optimizer" {
		t.Errorf("Component = %v, want optimizer", err.Component)
	}
}

func TestWrapInternalError(t *testing.T) {
	originalErr := errors.New("panic: nil pointer")
	wrappedErr := WrapInternalError(originalErr, "handler", "panic recovered")

	if wrappedErr.Code() != ErrCodeInternal {
		t.Errorf("Code() = %v, want %v", wrappedErr.Code(), ErrCodeInternal)
	}

	if wrappedErr.Component != "handler" {
		t.Errorf("Component = %v, want handler", wrappedErr.Component)
	}

	if !errors.Is(wrappedErr, originalErr) {
		t.Error("errors.Is() failed")
	}
}

func TestErrorTypeChecking(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		target     interface{}
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

	if dbErr.Operation != "query" {
		t.Errorf("DatabaseError.Operation = %v, want query", dbErr.Operation)
	}
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

	var loggedFields map[string]interface{}
	logError(loggerFunc(func(_ Level, _ error, fields map[string]interface{}, _ string) {
		loggedFields = fields
	}), wrapped, http.StatusBadRequest)

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
type loggerFunc func(level Level, err error, fields map[string]interface{}, msg string)

func (f loggerFunc) Log(level Level, err error, fields map[string]interface{}, msg string) {
	f(level, err, fields, msg)
}
