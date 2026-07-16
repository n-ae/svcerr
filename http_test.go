package svcerr

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPStatusCode(t *testing.T) {
	tests := []struct {
		code ErrorCode
		want int
	}{
		{ErrCodeInvalidInput, http.StatusBadRequest},
		{ErrCodeMissingRequired, http.StatusBadRequest},
		{ErrCodeInvalidFormat, http.StatusBadRequest},
		{ErrCodeConstraintViolation, http.StatusBadRequest},
		{ErrCodeUnauthorized, http.StatusUnauthorized},
		{ErrCodeTokenExpired, http.StatusUnauthorized},
		{ErrCodeTokenInvalid, http.StatusUnauthorized},
		{ErrCodePermissionDenied, http.StatusForbidden},
		{ErrCodeNotFound, http.StatusNotFound},
		{ErrCodeAlreadyExists, http.StatusConflict},
		{ErrCodeResourceConflict, http.StatusConflict},
		{ErrCodeRateLimitExceeded, http.StatusTooManyRequests},
		{ErrCodeQuotaExceeded, http.StatusTooManyRequests},
		{ErrCodeExternalAPI, http.StatusBadGateway},
		{ErrCodeDatabaseConnection, http.StatusServiceUnavailable},
		{ErrCodeDatabaseQuery, http.StatusInternalServerError},
		{ErrCodeDatabaseTransaction, http.StatusInternalServerError},
		{ErrCodeDatabaseMigration, http.StatusInternalServerError},
		{ErrCodeInternal, http.StatusInternalServerError},
		{ErrCodeNotImplemented, http.StatusNotImplemented},
		{ErrorCode("SOMETHING_UNKNOWN"), http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			if got := HTTPStatusCode(tt.code); got != tt.want {
				t.Errorf("HTTPStatusCode(%q) = %d, want %d", tt.code, got, tt.want)
			}
		})
	}
}

func TestRegisterStatusCode(t *testing.T) {
	const custom ErrorCode = "MY_APP_CUSTOM_CODE"
	t.Cleanup(func() {
		customStatusMu.Lock()
		delete(customStatusCode, custom)
		delete(customStatusCode, ErrCodeNotFound)
		customStatusMu.Unlock()
	})

	if got := HTTPStatusCode(custom); got != http.StatusInternalServerError {
		t.Fatalf("HTTPStatusCode(%q) before registering = %d, want the default %d", custom, got, http.StatusInternalServerError)
	}

	if err := RegisterStatusCode(custom, http.StatusTeapot); err != nil {
		t.Fatalf("RegisterStatusCode(%q, %d) error = %v", custom, http.StatusTeapot, err)
	}
	if got := HTTPStatusCode(custom); got != http.StatusTeapot {
		t.Errorf("HTTPStatusCode(%q) after registering = %d, want %d", custom, got, http.StatusTeapot)
	}

	// Registering a built-in code overrides it too.
	if err := RegisterStatusCode(ErrCodeNotFound, http.StatusTeapot); err != nil {
		t.Fatalf("RegisterStatusCode(ErrCodeNotFound, %d) error = %v", http.StatusTeapot, err)
	}
	if got := HTTPStatusCode(ErrCodeNotFound); got != http.StatusTeapot {
		t.Errorf("HTTPStatusCode(ErrCodeNotFound) after override = %d, want %d", got, http.StatusTeapot)
	}
}

func TestRegisterStatusCodeRejectsInvalidStatus(t *testing.T) {
	const custom ErrorCode = "MY_APP_INVALID_STATUS_CODE"
	t.Cleanup(func() {
		customStatusMu.Lock()
		delete(customStatusCode, custom)
		customStatusMu.Unlock()
	})

	for _, status := range []int{0, 200, 399, 600, 999} {
		if err := RegisterStatusCode(custom, status); err == nil {
			t.Errorf("RegisterStatusCode(%q, %d) error = nil, want an error (not a valid 400-599 error status)", custom, status)
		}
	}

	// A rejected registration must not have taken effect.
	if got := HTTPStatusCode(custom); got != http.StatusInternalServerError {
		t.Errorf("HTTPStatusCode(%q) = %d, want the default %d (no rejected registration should apply)", custom, got, http.StatusInternalServerError)
	}
}

// TestGetUserFriendlyMessageDefaultsByCode exercises the per-code default
// message switch in getUserFriendlyMessage directly (err = nil, so the
// function can't take the early "use the error's own text" path). Every
// declared ErrorCode is covered so a code added without a matching case -
// which would otherwise silently fall through to the generic default and
// go unnoticed - fails this test instead.
func TestGetUserFriendlyMessageDefaultsByCode(t *testing.T) {
	tests := []struct {
		code ErrorCode
		want string
	}{
		{ErrCodeInvalidInput, "Invalid input provided. Please check your request and try again."},
		{ErrCodeInvalidFormat, "Invalid input provided. Please check your request and try again."},
		{ErrCodeConstraintViolation, "Invalid input provided. Please check your request and try again."},
		{ErrCodeMissingRequired, "Required field is missing."},
		{ErrCodeUnauthorized, "Authentication required. Please log in."},
		{ErrCodeTokenExpired, "Your session has expired. Please log in again."},
		{ErrCodeTokenInvalid, "Invalid authentication token."},
		{ErrCodePermissionDenied, "You don't have permission to access this resource."},
		{ErrCodeNotFound, "The requested resource was not found."},
		{ErrCodeAlreadyExists, "A resource with this identifier already exists."},
		{ErrCodeResourceConflict, "The request conflicts with the current state of the resource."},
		{ErrCodeRateLimitExceeded, "Too many requests. Please try again later."},
		{ErrCodeQuotaExceeded, "You have exceeded your allotted quota."},
		{ErrCodeExternalAPI, "External service is temporarily unavailable. Please try again later."},
		{ErrCodeDatabaseConnection, "Database error occurred. Please try again."},
		{ErrCodeDatabaseQuery, "Database error occurred. Please try again."},
		{ErrCodeDatabaseTransaction, "Database error occurred. Please try again."},
		{ErrCodeDatabaseMigration, "Database error occurred. Please try again."},
		{ErrCodeInternal, "An internal error occurred. Please contact support if the problem persists."},
		{ErrCodeNotImplemented, "This functionality is not yet implemented."},
		{ErrorCode("SOMETHING_UNKNOWN"), "An unexpected error occurred."},
	}

	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			if got := getUserFriendlyMessage(tt.code, nil); got != tt.want {
				t.Errorf("getUserFriendlyMessage(%q, nil) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

func TestWriteHTTPErrorWithGenericConstructor(t *testing.T) {
	t.Run("New reaches a previously unreachable code", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		err := New(ErrCodeDatabaseConnection, "could not reach the database")

		WriteHTTPError(w, err, logger)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
		}

		var resp HTTPErrorResponse
		if decErr := json.Unmarshal(w.Body.Bytes(), &resp); decErr != nil {
			t.Fatalf("body is not valid JSON: %v", decErr)
		}
		if resp.Error.Code != ErrCodeDatabaseConnection {
			t.Errorf("Error.Code = %v, want %v", resp.Error.Code, ErrCodeDatabaseConnection)
		}
	})

	t.Run("Wrap does not leak the wrapped cause", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		secret := errors.New("password=hunter2 host=10.0.0.1")
		err := Wrap(secret, ErrCodeDatabaseMigration, "migration failed")

		WriteHTTPError(w, err, logger)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
		}
		if strings.Contains(w.Body.String(), "hunter2") {
			t.Errorf("response body leaked wrapped cause: %s", w.Body.String())
		}
	})
}

// recordingLogger captures every Log call for assertions.
type recordingLogger struct {
	calls []loggedCall
}

type loggedCall struct {
	level  Level
	err    error
	fields map[string]interface{}
	msg    string
}

func (l *recordingLogger) Log(level Level, err error, fields map[string]interface{}, msg string) {
	l.calls = append(l.calls, loggedCall{level: level, err: err, fields: fields, msg: msg})
}

func TestWriteHTTPError(t *testing.T) {
	t.Run("not found error", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		err := NewNotFoundError("league", "12345")

		WriteHTTPError(w, err, logger)

		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		var resp HTTPErrorResponse
		if decErr := json.Unmarshal(w.Body.Bytes(), &resp); decErr != nil {
			t.Fatalf("body is not valid JSON: %v (body: %s)", decErr, w.Body.String())
		}
		if resp.Error.Code != ErrCodeNotFound {
			t.Errorf("Error.Code = %v, want %v", resp.Error.Code, ErrCodeNotFound)
		}
		if resp.Error.Message != err.Error() {
			t.Errorf("Error.Message = %q, want %q", resp.Error.Message, err.Error())
		}
		if resp.Error.Details["resource_type"] != "league" {
			t.Errorf("Details[resource_type] = %v, want league", resp.Error.Details["resource_type"])
		}

		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1", len(logger.calls))
		}
		if logger.calls[0].level != LevelWarn {
			t.Errorf("logged level = %v, want LevelWarn (404 is a 4xx)", logger.calls[0].level)
		}
	})

	t.Run("external API error includes service details", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		retryAfter := 30
		err := NewExternalAPIError("yahoo", "yahoo API call failed", 503, "https://fantasysports.yahooapis.com/...")
		err.RetryAfter = &retryAfter

		WriteHTTPError(w, err, logger)

		if w.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadGateway)
		}

		var resp HTTPErrorResponse
		if decErr := json.Unmarshal(w.Body.Bytes(), &resp); decErr != nil {
			t.Fatalf("body is not valid JSON: %v (body: %s)", decErr, w.Body.String())
		}
		if resp.Error.Details["service"] != "yahoo" {
			t.Errorf("Details[service] = %v, want yahoo", resp.Error.Details["service"])
		}
		if resp.Error.Details["status_code"] != float64(503) {
			t.Errorf("Details[status_code] = %v, want 503", resp.Error.Details["status_code"])
		}
		if resp.Error.Details["retry_after"] != float64(30) {
			t.Errorf("Details[retry_after] = %v, want 30", resp.Error.Details["retry_after"])
		}

		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1", len(logger.calls))
		}
		if logger.calls[0].fields["service"] != "yahoo" {
			t.Errorf("logged service field = %v, want yahoo", logger.calls[0].fields["service"])
		}
		if logger.calls[0].fields["service_status"] != 503 {
			t.Errorf("logged service_status field = %v, want 503", logger.calls[0].fields["service_status"])
		}
	})

	t.Run("authentication error logs the auth reason", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		err := NewAuthenticationError("token_expired", "session expired")

		WriteHTTPError(w, err, logger)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1", len(logger.calls))
		}
		if logger.calls[0].fields["auth_reason"] != "token_expired" {
			t.Errorf("logged auth_reason field = %v, want token_expired", logger.calls[0].fields["auth_reason"])
		}
	})

	t.Run("rate limit error sets Retry-After header", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		err := NewRateLimitError("yahoo", 300, 60)

		WriteHTTPError(w, err, logger)

		if w.Code != http.StatusTooManyRequests {
			t.Errorf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
		}
		if got := w.Header().Get("Retry-After"); got != "60" {
			t.Errorf("Retry-After = %q, want 60", got)
		}
	})

	t.Run("rate limit error wrapped by a plain stdlib wrapper still sets Retry-After header", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		inner := NewRateLimitError("yahoo", 300, 60)
		wrapped := fmt.Errorf("propagated: %w", inner)

		WriteHTTPError(w, wrapped, logger)

		if got := w.Header().Get("Retry-After"); got != "60" {
			t.Errorf("Retry-After = %q, want 60 (fmt.Errorf doesn't establish a new code, so the RateLimitError is still the outermost coded node)", got)
		}
	})

	t.Run("rate limit error wrapped under a different code does not leak Retry-After header", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		inner := NewRateLimitError("yahoo", 300, 60)
		wrapped := WrapInternalError(inner, "handler", "propagated")

		WriteHTTPError(w, wrapped, logger)

		if got := w.Header().Get("Retry-After"); got != "" {
			t.Errorf("Retry-After = %q, want empty (outer code is ErrCodeInternal, so the inner RateLimitError's header must not leak)", got)
		}
	})

	t.Run("wrapping under a different code hides the inner error's public details", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		inner := NewNotFoundError("user", "secret@example.com")
		wrapped := WrapInternalError(inner, "user_service", "unexpected repository result")

		WriteHTTPError(w, wrapped, logger)

		var resp HTTPErrorResponse
		if decErr := json.Unmarshal(w.Body.Bytes(), &resp); decErr != nil {
			t.Fatalf("body is not valid JSON: %v", decErr)
		}
		if resp.Error.Code != ErrCodeInternal {
			t.Errorf("Error.Code = %q, want %q", resp.Error.Code, ErrCodeInternal)
		}
		if resp.Error.Details != nil {
			t.Errorf("Error.Details = %v, want nil (the inner NotFoundError's resource_type/resource_id must not leak through the outer INTERNAL_ERROR classification)", resp.Error.Details)
		}
	})

	t.Run("SetPublicMessage overrides the response message", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		err := NewDatabaseError("query", "pq: connection to 10.0.4.12:5432 refused")
		err.SetPublicMessage("We're having trouble reaching the database. Please try again shortly.")

		WriteHTTPError(w, err, logger)

		var resp HTTPErrorResponse
		if decErr := json.Unmarshal(w.Body.Bytes(), &resp); decErr != nil {
			t.Fatalf("body is not valid JSON: %v", decErr)
		}
		if resp.Error.Message != "We're having trouble reaching the database. Please try again shortly." {
			t.Errorf("Error.Message = %q, want the public message override, not the internal detail", resp.Error.Message)
		}
	})

	t.Run("no Retry-After header for non-rate-limit errors", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}

		WriteHTTPError(w, NewNotFoundError("league", "1"), logger)

		if got := w.Header().Get("Retry-After"); got != "" {
			t.Errorf("Retry-After = %q, want empty", got)
		}
	})

	t.Run("internal error logs at error level with stack trace", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		err := NewInternalError("optimizer", "unexpected")

		WriteHTTPError(w, err, logger)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
		}
		if len(logger.calls) != 1 || logger.calls[0].level != LevelError {
			t.Fatalf("expected one LevelError log call, got %+v", logger.calls)
		}
		if _, ok := logger.calls[0].fields["stack_trace"]; !ok {
			t.Error("expected stack_trace field for a 500-level error")
		}
	})

	t.Run("plain stdlib error defaults to internal error", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}

		WriteHTTPError(w, stdlibError("boom"), logger)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
		}

		var resp HTTPErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("body is not valid JSON: %v", err)
		}
		if resp.Error.Code != ErrCodeInternal {
			t.Errorf("Error.Code = %v, want %v", resp.Error.Code, ErrCodeInternal)
		}
	})
}

// stdlibError is a bare error type (not *BaseError-derived), to exercise the
// "unknown error" fallback path independent of this package's own New()/
// stdlib errors.New value type.
type stdlibError string

func (e stdlibError) Error() string { return string(e) }

func TestWriteHTTPErrorHTML(t *testing.T) {
	w := httptest.NewRecorder()
	logger := &recordingLogger{}
	err := NewValidationError("invalid email", "email", "not-an-email")

	WriteHTTPErrorHTML(w, err, logger)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, `class="error-message"`) {
		t.Errorf("body missing error-message div: %s", body)
	}
	if !strings.Contains(body, err.Error()) {
		t.Errorf("body missing error message %q: %s", err.Error(), body)
	}

	if len(logger.calls) != 1 {
		t.Fatalf("logger.calls = %d, want 1", len(logger.calls))
	}
}

func TestWriteHTTPProblem(t *testing.T) {
	t.Run("standard members plus flattened extensions", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		err := NewNotFoundError("league", "12345")

		WriteHTTPProblem(w, err, logger)

		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("Content-Type = %q, want application/problem+json", ct)
		}

		var resp map[string]interface{}
		if decErr := json.Unmarshal(w.Body.Bytes(), &resp); decErr != nil {
			t.Fatalf("body is not valid JSON: %v (body: %s)", decErr, w.Body.String())
		}
		if resp["type"] != "about:blank" {
			t.Errorf(`resp["type"] = %v, want "about:blank"`, resp["type"])
		}
		if resp["title"] != "Not Found" {
			t.Errorf(`resp["title"] = %v, want the HTTP status's reason phrase (RFC 9457 4.2.1, since type is "about:blank")`, resp["title"])
		}
		if resp["status"] != float64(http.StatusNotFound) {
			t.Errorf(`resp["status"] = %v, want %d`, resp["status"], http.StatusNotFound)
		}
		if resp["detail"] != "league not found: 12345" {
			t.Errorf(`resp["detail"] = %v, want "league not found: 12345"`, resp["detail"])
		}
		if resp["code"] != string(ErrCodeNotFound) {
			t.Errorf(`resp["code"] = %v, want %q`, resp["code"], ErrCodeNotFound)
		}
		if resp["resource_type"] != "league" {
			t.Errorf(`resp["resource_type"] = %v, want "league" (extractErrorDetails' extension members flattened to top level)`, resp["resource_type"])
		}
		if _, ok := resp["instance"]; ok {
			t.Errorf(`resp["instance"] = %v, want the key omitted entirely when unset`, resp["instance"])
		}

		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1", len(logger.calls))
		}
	})

	t.Run("does not leak the wrapped cause", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		secret := errors.New("password=hunter2 host=10.0.0.1")
		err := WrapDatabaseError(secret, "query", "SELECT * FROM users")

		WriteHTTPProblem(w, err, logger)

		if strings.Contains(w.Body.String(), "hunter2") {
			t.Errorf("response body leaked wrapped cause: %s", w.Body.String())
		}
	})

	t.Run("SetProblemType/SetProblemInstance override the response", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		err := NewNotFoundError("league", "12345")
		err.SetProblemType("https://example.com/problems/resource-not-found")
		err.SetProblemInstance("https://example.com/requests/abc123")

		WriteHTTPProblem(w, err, logger)

		var resp map[string]interface{}
		if decErr := json.Unmarshal(w.Body.Bytes(), &resp); decErr != nil {
			t.Fatalf("body is not valid JSON: %v", decErr)
		}
		if resp["type"] != "https://example.com/problems/resource-not-found" {
			t.Errorf(`resp["type"] = %v, want the SetProblemType override`, resp["type"])
		}
		if resp["instance"] != "https://example.com/requests/abc123" {
			t.Errorf(`resp["instance"] = %v, want the SetProblemInstance override`, resp["instance"])
		}
		// Title stays the status's reason phrase regardless of a custom
		// Type - WriteHTTPProblem has no per-error Title override.
		if resp["title"] != "Not Found" {
			t.Errorf(`resp["title"] = %v, want "Not Found"`, resp["title"])
		}
	})

	t.Run("rate limit error sets Retry-After header", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		err := NewRateLimitError("yahoo", 300, 60)

		WriteHTTPProblem(w, err, logger)

		if got := w.Header().Get("Retry-After"); got != "60" {
			t.Errorf("Retry-After = %q, want 60", got)
		}
	})
}

func TestWriteHTTPErrorHTMLEscapesMessage(t *testing.T) {
	w := httptest.NewRecorder()
	logger := &recordingLogger{}
	err := NewValidationError(`<script>alert("xss")</script>`, "field", nil)

	WriteHTTPErrorHTML(w, err, logger)

	body := w.Body.String()
	if strings.Contains(body, "<script>") {
		t.Errorf("body contains unescaped script tag: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("body missing escaped message: %s", body)
	}
}

func TestWriteHTTPErrorToleratesNilLogger(t *testing.T) {
	// A nil Logger must not panic - callers who only want the response
	// rendered (no logging contract) can pass nil instead of a no-op
	// implementation.
	for name, write := range map[string]func(http.ResponseWriter, error, Logger){
		"WriteHTTPError":     WriteHTTPError,
		"WriteHTTPErrorHTML": WriteHTTPErrorHTML,
		"WriteHTTPProblem":   WriteHTTPProblem,
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			write(w, NewNotFoundError("league", "1"), nil)
			if w.Code != http.StatusNotFound {
				t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
			}
		})
	}
}

func TestRecoveryMiddlewareToleratesNilLogger(t *testing.T) {
	handler := RecoveryMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestPureRenderFunctions(t *testing.T) {
	// WriteJSON/WriteHTML/WriteProblem mirror their WriteHTTP*
	// counterparts' body and status exactly, minus the logging call.
	err := NewNotFoundError("league", "1")

	t.Run("WriteJSON", func(t *testing.T) {
		w := httptest.NewRecorder()
		status := WriteJSON(w, err)

		logged := httptest.NewRecorder()
		WriteHTTPError(logged, err, &recordingLogger{})

		if status != http.StatusNotFound {
			t.Errorf("status = %d, want %d", status, http.StatusNotFound)
		}
		if w.Body.String() != logged.Body.String() {
			t.Errorf("WriteJSON body = %q, want the same body as WriteHTTPError: %q", w.Body.String(), logged.Body.String())
		}
	})

	t.Run("WriteHTML", func(t *testing.T) {
		w := httptest.NewRecorder()
		status := WriteHTML(w, err)

		logged := httptest.NewRecorder()
		WriteHTTPErrorHTML(logged, err, &recordingLogger{})

		if status != http.StatusNotFound {
			t.Errorf("status = %d, want %d", status, http.StatusNotFound)
		}
		if w.Body.String() != logged.Body.String() {
			t.Errorf("WriteHTML body = %q, want the same body as WriteHTTPErrorHTML: %q", w.Body.String(), logged.Body.String())
		}
	})

	t.Run("WriteProblem", func(t *testing.T) {
		w := httptest.NewRecorder()
		status := WriteProblem(w, err)

		logged := httptest.NewRecorder()
		WriteHTTPProblem(logged, err, &recordingLogger{})

		if status != http.StatusNotFound {
			t.Errorf("status = %d, want %d", status, http.StatusNotFound)
		}
		if w.Body.String() != logged.Body.String() {
			t.Errorf("WriteProblem body = %q, want the same body as WriteHTTPProblem: %q", w.Body.String(), logged.Body.String())
		}
	})
}

func TestWrappedInternalDetailNotLeakedToClient(t *testing.T) {
	t.Run("WriteHTTPError", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		secret := errors.New("password=hunter2 host=10.0.0.1")
		err := WrapDatabaseError(secret, "query", "SELECT * FROM users")

		WriteHTTPError(w, err, logger)

		if strings.Contains(w.Body.String(), "hunter2") {
			t.Errorf("response body leaked wrapped cause: %s", w.Body.String())
		}
	})

	t.Run("panic(error) via RecoveryMiddleware", func(t *testing.T) {
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(errors.New("password=hunter2 host=10.0.0.1"))
		}))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

		if strings.Contains(w.Body.String(), "hunter2") {
			t.Errorf("response body leaked panic value: %s", w.Body.String())
		}
	})

	t.Run("panic(string) via RecoveryMiddleware", func(t *testing.T) {
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("password=hunter2 host=10.0.0.1")
		}))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

		if strings.Contains(w.Body.String(), "hunter2") {
			t.Errorf("response body leaked panic value: %s", w.Body.String())
		}
	})

	t.Run("ValidationError.Value", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		err := NewValidationError("invalid password", "password", "hunter2")

		WriteHTTPError(w, err, logger)

		if strings.Contains(w.Body.String(), "hunter2") {
			t.Errorf("response body leaked ValidationError.Value: %s", w.Body.String())
		}

		var resp HTTPErrorResponse
		if decErr := json.Unmarshal(w.Body.Bytes(), &resp); decErr != nil {
			t.Fatalf("body is not valid JSON: %v", decErr)
		}
		if _, ok := resp.Error.Details["value"]; ok {
			t.Errorf("Details contains \"value\" key, want it omitted entirely: %+v", resp.Error.Details)
		}
		if resp.Error.Details["field"] != "password" {
			t.Errorf("Details[field] = %v, want password (field name is still safe to include)", resp.Error.Details["field"])
		}
	})
}

// hijackableRecorder wraps httptest.ResponseRecorder (which doesn't
// implement http.Hijacker) to also support hijacking, for testing
// trackingResponseWriter's passthrough.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	server, _ := net.Pipe()
	return server, bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)), nil
}

func TestTrackingResponseWriterFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	tw := &trackingResponseWriter{ResponseWriter: rec}

	f, ok := (http.ResponseWriter)(tw).(http.Flusher)
	if !ok {
		t.Fatal("trackingResponseWriter should implement http.Flusher")
	}
	f.Flush()

	if !rec.Flushed {
		t.Error("Flush() did not delegate to the underlying ResponseWriter")
	}
	if !tw.wroteHeader {
		t.Error("Flush() should mark the response committed, the same way Write/WriteHeader do, so RecoveryMiddleware won't append a second body after a later panic")
	}
	if tw.status != http.StatusOK {
		t.Errorf("tw.status = %d, want %d (Flush with no prior WriteHeader implies 200)", tw.status, http.StatusOK)
	}
}

// nonFlushingWriter implements only http.ResponseWriter - deliberately not
// http.Flusher - to verify trackingResponseWriter.Flush() doesn't mark the
// response committed when the underlying writer can't actually flush.
type nonFlushingWriter struct {
	rec *httptest.ResponseRecorder
}

func (w *nonFlushingWriter) Header() http.Header         { return w.rec.Header() }
func (w *nonFlushingWriter) Write(b []byte) (int, error) { return w.rec.Write(b) }
func (w *nonFlushingWriter) WriteHeader(statusCode int)  { w.rec.WriteHeader(statusCode) }

func TestTrackingResponseWriterFlushOnUnsupportedUnderlyingWriter(t *testing.T) {
	underlying := &nonFlushingWriter{rec: httptest.NewRecorder()}
	tw := &trackingResponseWriter{ResponseWriter: underlying}

	// The wrapper structurally implements Flusher regardless - Go's
	// http.Flusher has no way to express "unsupported" via a type
	// assertion - so the property under test is that Flush() must not
	// mark the response committed when nothing was actually flushed.
	f, ok := (http.ResponseWriter)(tw).(http.Flusher)
	if !ok {
		t.Fatal("trackingResponseWriter should structurally implement http.Flusher regardless")
	}
	f.Flush()

	if tw.wroteHeader {
		t.Error("Flush() marked the response committed even though the underlying writer doesn't support http.Flusher - nothing was actually written, so RecoveryMiddleware would wrongly skip writing the real error response after a later panic")
	}
}

func TestTrackingResponseWriterInformationalHeaderIsNotCommitment(t *testing.T) {
	rec := httptest.NewRecorder()
	tw := &trackingResponseWriter{ResponseWriter: rec}

	tw.WriteHeader(http.StatusEarlyHints) // 103
	if tw.wroteHeader {
		t.Errorf("WriteHeader(103) marked the response committed (status=%d) - a 1xx informational header isn't the final response, so a later panic still needs RecoveryMiddleware to write the real error response", tw.status)
	}

	tw.WriteHeader(http.StatusOK)
	if !tw.wroteHeader || tw.status != http.StatusOK {
		t.Errorf("wroteHeader=%v status=%d after a real final WriteHeader, want true/%d", tw.wroteHeader, tw.status, http.StatusOK)
	}
}

func TestTrackingResponseWriterIgnoresRepeatedFinalWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	tw := &trackingResponseWriter{ResponseWriter: rec}

	tw.WriteHeader(http.StatusNotFound)
	tw.WriteHeader(http.StatusInternalServerError) // must be a no-op: the first final status already committed

	if tw.status != http.StatusNotFound {
		t.Errorf("tw.status = %d, want %d (the first final WriteHeader call should stick)", tw.status, http.StatusNotFound)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("rec.Code = %d, want %d (a repeated final WriteHeader must not reach the underlying writer)", rec.Code, http.StatusNotFound)
	}
}

func TestTrackingResponseWriterSwitchingProtocolsIsCommitment(t *testing.T) {
	rec := httptest.NewRecorder()
	tw := &trackingResponseWriter{ResponseWriter: rec}

	tw.WriteHeader(http.StatusSwitchingProtocols)
	if !tw.wroteHeader || tw.status != http.StatusSwitchingProtocols {
		t.Errorf("wroteHeader=%v status=%d after WriteHeader(101), want true/%d (a protocol transition, not an informational preamble)", tw.wroteHeader, tw.status, http.StatusSwitchingProtocols)
	}
}

func TestTrackingResponseWriterUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	tw := &trackingResponseWriter{ResponseWriter: rec}

	u, ok := (http.ResponseWriter)(tw).(interface{ Unwrap() http.ResponseWriter })
	if !ok {
		t.Fatal("trackingResponseWriter should implement Unwrap() http.ResponseWriter for http.ResponseController")
	}
	if u.Unwrap() != rec {
		t.Error("Unwrap() did not return the underlying ResponseWriter")
	}
}

func TestTrackingResponseWriterHijack(t *testing.T) {
	t.Run("underlying writer does not support hijacking", func(t *testing.T) {
		rec := httptest.NewRecorder()
		tw := &trackingResponseWriter{ResponseWriter: rec}

		hj, ok := (http.ResponseWriter)(tw).(http.Hijacker)
		if !ok {
			t.Fatal("trackingResponseWriter should structurally implement http.Hijacker")
		}
		if _, _, err := hj.Hijack(); err == nil {
			t.Error("Hijack() error = nil, want an error since the underlying writer doesn't support hijacking")
		}
	})

	t.Run("successful hijack delegates and marks the response committed", func(t *testing.T) {
		underlying := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
		tw := &trackingResponseWriter{ResponseWriter: underlying}

		hj := (http.ResponseWriter)(tw).(http.Hijacker)
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("Hijack() error = %v", err)
		}
		defer func() { _ = conn.Close() }()

		if !underlying.hijacked {
			t.Error("Hijack() did not delegate to the underlying ResponseWriter")
		}
		if !tw.wroteHeader {
			t.Error("a successful Hijack() should mark the response committed, so RecoveryMiddleware won't write a JSON body over the now-raw connection")
		}
	})
}

func TestRecoveryMiddleware(t *testing.T) {
	t.Run("recovers panic(error)", func(t *testing.T) {
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(stdlibError("boom"))
		}))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/leagues", nil))

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
		}

		var resp HTTPErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("body is not valid JSON: %v", err)
		}
		if resp.Error.Code != ErrCodeInternal {
			t.Errorf("Error.Code = %v, want %v", resp.Error.Code, ErrCodeInternal)
		}

		// A single log call carrying both the panic context (method, path,
		// panic value) and the usual error fields (error_code, http_status,
		// stack_trace) - not one from RecoveryMiddleware and a second,
		// separate one from WriteHTTPError's own logging.
		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1, got %+v", len(logger.calls), logger.calls)
		}
		if logger.calls[0].msg != "Panic recovered in HTTP handler" {
			t.Errorf("log msg = %q, want %q", logger.calls[0].msg, "Panic recovered in HTTP handler")
		}
		if logger.calls[0].fields["method"] != http.MethodGet {
			t.Errorf("method field = %v, want GET", logger.calls[0].fields["method"])
		}
		if logger.calls[0].fields["path"] != "/leagues" {
			t.Errorf("path field = %v, want /leagues", logger.calls[0].fields["path"])
		}
		if logger.calls[0].fields["error_code"] != string(ErrCodeInternal) {
			t.Errorf("error_code field = %v, want %v", logger.calls[0].fields["error_code"], ErrCodeInternal)
		}
		if logger.calls[0].fields["http_status"] != http.StatusInternalServerError {
			t.Errorf("http_status field = %v, want %v", logger.calls[0].fields["http_status"], http.StatusInternalServerError)
		}
		if _, ok := logger.calls[0].fields["stack_trace"]; !ok {
			t.Error("expected stack_trace field on the panic log")
		}
	})

	t.Run("recovers panic(string)", func(t *testing.T) {
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("something went wrong")
		}))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
		}
	})

	t.Run("recovers panic(other type)", func(t *testing.T) {
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(42)
		}))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
		}
	})

	t.Run("no panic, request passes through untouched", func(t *testing.T) {
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

		if w.Code != http.StatusTeapot {
			t.Errorf("status = %d, want %d", w.Code, http.StatusTeapot)
		}
		if len(logger.calls) != 0 {
			t.Errorf("logger.calls = %d, want 0 (no panic occurred)", len(logger.calls))
		}
	})

	t.Run("does not swallow http.ErrAbortHandler", func(t *testing.T) {
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(http.ErrAbortHandler)
		}))

		w := httptest.NewRecorder()

		func() {
			defer func() {
				rec := recover()
				if rec != http.ErrAbortHandler {
					t.Errorf("recovered %v, want it to re-panic with http.ErrAbortHandler", rec)
				}
			}()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
			t.Error("ServeHTTP returned normally, want it to panic with http.ErrAbortHandler")
		}()

		if len(logger.calls) != 0 {
			t.Errorf("logger.calls = %d, want 0 (ErrAbortHandler should not be logged as an error)", len(logger.calls))
		}
	})

	t.Run("response already committed before panic is not appended to", func(t *testing.T) {
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			panic("boom after commit")
		}))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

		// The 200 already sent to the client can't be retracted - the
		// recorder keeps whatever status was written first.
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d (already committed, can't be changed)", w.Code, http.StatusOK)
		}
		if got, want := w.Body.String(), `{"ok":true}`; got != want {
			t.Errorf("body = %q, want %q (no error JSON should be appended to a committed response)", got, want)
		}

		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1, got %+v", len(logger.calls), logger.calls)
		}
		if logger.calls[0].msg != "Panic recovered in HTTP handler after response was already committed" {
			t.Errorf("log msg = %q, want the committed-response variant", logger.calls[0].msg)
		}
		if logger.calls[0].fields["response_committed_status"] != http.StatusOK {
			t.Errorf("response_committed_status field = %v, want %v", logger.calls[0].fields["response_committed_status"], http.StatusOK)
		}
	})

	t.Run("response committed via Write without WriteHeader is not appended to", func(t *testing.T) {
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// No explicit WriteHeader call - Write alone commits an
			// implicit 200, same as the stdlib http.ResponseWriter.
			_, _ = w.Write([]byte("partial"))
			panic("boom after implicit 200")
		}))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d (Write without WriteHeader implies 200)", w.Code, http.StatusOK)
		}
		if got, want := w.Body.String(), "partial"; got != want {
			t.Errorf("body = %q, want %q (no error JSON should be appended to a committed response)", got, want)
		}

		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1, got %+v", len(logger.calls), logger.calls)
		}
		if logger.calls[0].msg != "Panic recovered in HTTP handler after response was already committed" {
			t.Errorf("log msg = %q, want the committed-response variant", logger.calls[0].msg)
		}
		if logger.calls[0].fields["response_committed_status"] != http.StatusOK {
			t.Errorf("response_committed_status field = %v, want %v (Write alone should default the tracked status to 200)", logger.calls[0].fields["response_committed_status"], http.StatusOK)
		}
	})

	t.Run("response committed via Flush without WriteHeader is not appended to", func(t *testing.T) {
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// No explicit WriteHeader or Write - just a flush, which
			// commits an implicit 200 the same way Write does.
			w.(http.Flusher).Flush()
			panic("boom after flush")
		}))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

		if got := w.Body.String(); got != "" {
			t.Errorf("body = %q, want empty (Flush commits the response, so no error JSON should be appended on top of it)", got)
		}

		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1, got %+v", len(logger.calls), logger.calls)
		}
		if logger.calls[0].msg != "Panic recovered in HTTP handler after response was already committed" {
			t.Errorf("log msg = %q, want the committed-response variant", logger.calls[0].msg)
		}
		if logger.calls[0].fields["response_committed_status"] != http.StatusOK {
			t.Errorf("response_committed_status field = %v, want %v (Flush alone should default the tracked status to 200)", logger.calls[0].fields["response_committed_status"], http.StatusOK)
		}
	})

	t.Run("panic after Flush on a non-flushing writer still gets the error response", func(t *testing.T) {
		logger := &recordingLogger{}
		underlying := &nonFlushingWriter{rec: httptest.NewRecorder()}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The tracking wrapper structurally implements http.Flusher
			// regardless of what the underlying writer supports, so this
			// assertion succeeds - but nothing is actually flushed.
			w.(http.Flusher).Flush()
			panic("boom after a no-op flush")
		}))

		handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))

		if underlying.rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d (a no-op Flush on a non-flushing writer must not be treated as committing the response)", underlying.rec.Code, http.StatusInternalServerError)
		}
		var resp HTTPErrorResponse
		if err := json.Unmarshal(underlying.rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("body is not valid JSON: %v (body: %s)", err, underlying.rec.Body.String())
		}
		if resp.Error.Code != ErrCodeInternal {
			t.Errorf("Error.Code = %v, want %v", resp.Error.Code, ErrCodeInternal)
		}
	})

	t.Run("panic after an informational 1xx header still gets the error response", func(t *testing.T) {
		// httptest.ResponseRecorder, unlike a real net/http server
		// connection, has no special handling for repeated WriteHeader
		// calls carrying a 1xx status - it just latches its Code/body on
		// the first call regardless of status, so it can't faithfully
		// stand in for what a real client would receive here. What this
		// test can verify reliably is RecoveryMiddleware's own decision:
		// the log message it chose proves whether it believed the
		// response was still safe to write to (the plain message) or
		// already committed (the committed-response variant).
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusEarlyHints) // 103 - not the final response
			panic("boom after an informational header")
		}))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1, got %+v", len(logger.calls), logger.calls)
		}
		if logger.calls[0].msg != "Panic recovered in HTTP handler" {
			t.Errorf("log msg = %q, want the not-yet-committed variant (a 1xx informational header must not be treated as the final committed response)", logger.calls[0].msg)
		}
	})

	t.Run("hijacked connection is not written to on a later panic", func(t *testing.T) {
		logger := &recordingLogger{}
		underlying := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("handler's ResponseWriter does not implement http.Hijacker")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("Hijack() error = %v", err)
			}
			_ = conn.Close()
			panic("boom after hijack")
		}))

		handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))

		if underlying.Body.Len() != 0 {
			t.Errorf("body = %q, want empty (nothing should be written to a hijacked connection)", underlying.Body.String())
		}
		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1, got %+v", len(logger.calls), logger.calls)
		}
		if logger.calls[0].msg != "Panic recovered in HTTP handler after response was already committed" {
			t.Errorf("log msg = %q, want the committed-response variant", logger.calls[0].msg)
		}
	})
}
