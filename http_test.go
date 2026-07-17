package svcerr

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
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

// failingWriter is an http.ResponseWriter whose Write always fails, as a
// real one's would on a client disconnect, an expired write deadline, or
// any other transport failure - the header map still works normally so
// this package's header-manipulation code runs unaffected.
type failingWriter struct {
	header http.Header
	status int
}

func (w *failingWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func (w *failingWriter) WriteHeader(status int) {
	w.status = status
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

	t.Run("logged fields describe the same node as the logged code, not a different node in the chain", func(t *testing.T) {
		// Assessment 0008/L2: errorLogFields used to find type-specific
		// fields via independent errors.As calls across the whole chain,
		// so an outer NotFoundError's code/status could be logged
		// alongside an inner DatabaseError's db_operation field instead of
		// the outer node's own resource_type/resource_id - the same
		// code/details mismatch outermostCoded's doc comment warns
		// against, just one function over from where it was already fixed
		// for the response body (extractErrorDetails, exercised above).
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		inner := NewDatabaseError("query", "repository query failed")
		outer := WrapNotFoundError(inner, "user", "123")

		WriteHTTPError(w, outer, logger)

		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1", len(logger.calls))
		}
		fields := logger.calls[0].fields
		if fields["error_code"] != string(ErrCodeNotFound) {
			t.Errorf("logged error_code = %v, want %v", fields["error_code"], ErrCodeNotFound)
		}
		if fields["resource_type"] != "user" {
			t.Errorf("logged resource_type = %v, want %q (from the outer NotFoundError, the same node the code came from)", fields["resource_type"], "user")
		}
		if fields["resource_id"] != "123" {
			t.Errorf("logged resource_id = %v, want %q (from the outer NotFoundError, the same node the code came from)", fields["resource_id"], "123")
		}
		if _, ok := fields["db_operation"]; ok {
			t.Errorf("logged fields contain db_operation = %v, want it absent (that belongs to the inner DatabaseError, not the outer NotFoundError that produced error_code/http_status)", fields["db_operation"])
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

	t.Run("stale Retry-After/WWW-Authenticate from a previous response don't survive onto an unrelated one", func(t *testing.T) {
		w := httptest.NewRecorder()
		w.Header().Set("Retry-After", "999")
		w.Header().Set("WWW-Authenticate", `Basic realm="old"`)
		logger := &recordingLogger{}

		WriteHTTPError(w, NewNotFoundError("league", "1"), logger)

		if got := w.Header().Get("Retry-After"); got != "" {
			t.Errorf("Retry-After = %q, want empty (a 404 doesn't qualify for it, and the value predates this response)", got)
		}
		if got := w.Header().Get("WWW-Authenticate"); got != "" {
			t.Errorf("WWW-Authenticate = %q, want empty (a 404 doesn't qualify for it, and the value predates this response)", got)
		}
	})

	t.Run("a failed body write is logged, not silently swallowed", func(t *testing.T) {
		w := &failingWriter{}
		logger := &recordingLogger{}

		WriteHTTPError(w, NewNotFoundError("league", "1"), logger)

		if w.status != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.status, http.StatusNotFound)
		}
		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1", len(logger.calls))
		}
		if _, ok := logger.calls[0].fields["response_write_error"]; !ok {
			t.Error("expected a response_write_error field - the client never received a body and nothing else says so")
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

func TestWriteHTTPErrorHTMLSetsRetryAfterHeader(t *testing.T) {
	// Assessment 0008/L4: writeHTMLErrorBody never called
	// rateLimitRetryAfterHeader, so HTML 429 responses silently dropped
	// Retry-After that WriteHTTPError/WriteHTTPProblem both preserve for
	// the identical error (see "rate limit error sets Retry-After header"
	// above and in TestWriteHTTPProblem).
	w := httptest.NewRecorder()
	logger := &recordingLogger{}
	err := NewRateLimitError("yahoo", 300, 60)

	WriteHTTPErrorHTML(w, err, logger)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}
	if got := w.Header().Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want 60", got)
	}
}

func TestWriteHTTPErrorHTMLLogsWriteFailure(t *testing.T) {
	w := &failingWriter{}
	logger := &recordingLogger{}

	WriteHTTPErrorHTML(w, NewNotFoundError("league", "1"), logger)

	if len(logger.calls) != 1 {
		t.Fatalf("logger.calls = %d, want 1", len(logger.calls))
	}
	if _, ok := logger.calls[0].fields["response_write_error"]; !ok {
		t.Error("expected a response_write_error field - the client never received a body and nothing else says so")
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
		// Title stays the status's reason phrase absent a SetProblemTitle
		// override, even alongside a custom Type.
		if resp["title"] != "Not Found" {
			t.Errorf(`resp["title"] = %v, want "Not Found"`, resp["title"])
		}
	})

	t.Run("SetProblemTitle overrides the title", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		err := NewNotFoundError("league", "12345")
		err.SetProblemType("https://example.com/problems/resource-not-found")
		err.SetProblemTitle("League not found")

		WriteHTTPProblem(w, err, logger)

		var resp map[string]interface{}
		if decErr := json.Unmarshal(w.Body.Bytes(), &resp); decErr != nil {
			t.Fatalf("body is not valid JSON: %v", decErr)
		}
		if resp["title"] != "League not found" {
			t.Errorf(`resp["title"] = %v, want the SetProblemTitle override`, resp["title"])
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

	t.Run("a failed body write is logged, not silently swallowed", func(t *testing.T) {
		w := &failingWriter{}
		logger := &recordingLogger{}

		WriteHTTPProblem(w, NewNotFoundError("league", "1"), logger)

		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1", len(logger.calls))
		}
		if _, ok := logger.calls[0].fields["response_write_error"]; !ok {
			t.Error("expected a response_write_error field - the client never received a body and nothing else says so")
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

func TestWWWAuthenticateHeader(t *testing.T) {
	writers := map[string]func(http.ResponseWriter, error, Logger){
		"WriteHTTPError":     WriteHTTPError,
		"WriteHTTPErrorHTML": WriteHTTPErrorHTML,
		"WriteHTTPProblem":   WriteHTTPProblem,
	}

	for name, write := range writers {
		t.Run(name+"/challenge set", func(t *testing.T) {
			w := httptest.NewRecorder()
			err := NewAuthenticationError("token_invalid", "invalid authentication token")
			err.SetAuthenticateChallenge(`Bearer realm="api"`)

			write(w, err, nil)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
			}
			if got := w.Header().Get("WWW-Authenticate"); got != `Bearer realm="api"` {
				t.Errorf(`WWW-Authenticate = %q, want the SetAuthenticateChallenge value`, got)
			}
		})

		t.Run(name+"/no challenge set", func(t *testing.T) {
			w := httptest.NewRecorder()
			write(w, NewAuthenticationError("token_invalid", "invalid authentication token"), nil)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
			}
			if got := w.Header().Get("WWW-Authenticate"); got != "" {
				t.Errorf("WWW-Authenticate = %q, want empty (this package can't invent an application's auth scheme unless SetAuthenticateChallenge is called)", got)
			}
		})

		t.Run(name+"/challenge set but status isn't 401", func(t *testing.T) {
			w := httptest.NewRecorder()
			err := NewNotFoundError("league", "1")
			err.SetAuthenticateChallenge(`Bearer realm="api"`) // misuse: irrelevant code

			write(w, err, nil)

			if got := w.Header().Get("WWW-Authenticate"); got != "" {
				t.Errorf("WWW-Authenticate = %q, want empty (only applied to a 401 response)", got)
			}
		})

		t.Run(name+"/401 via a non-BaseError Coder that isn't an Authenticator", func(t *testing.T) {
			w := httptest.NewRecorder()
			write(w, &minimalCodedUnwrappableError{code: ErrCodeUnauthorized, msg: "unauthorized"}, nil)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
			}
			if got := w.Header().Get("WWW-Authenticate"); got != "" {
				t.Errorf("WWW-Authenticate = %q, want empty (a Coder that doesn't implement Authenticator can't provide one)", got)
			}
		})
	}
}

func TestPrepareErrorHeadersClearsStaleSuccessHeaders(t *testing.T) {
	// A handler that set headers for a would-be successful response (a
	// precomputed Content-Length, a Trailer announcement) before panicking
	// or returning an error must not have those survive onto the actual
	// error body - the same reasoning as net/http's own http.Error, which
	// deletes Content-Length for exactly this case.
	for name, write := range map[string]func(http.ResponseWriter, error, Logger){
		"WriteHTTPError":     WriteHTTPError,
		"WriteHTTPErrorHTML": WriteHTTPErrorHTML,
		"WriteHTTPProblem":   WriteHTTPProblem,
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			w.Header().Set("Content-Length", "999")
			w.Header().Set("Trailer", "X-Would-Have-Been-A-Trailer")
			w.Header().Set("Content-Encoding", "gzip")

			write(w, NewInternalError("test", "boom"), nil)

			if got := w.Header().Get("Content-Length"); got != "" {
				t.Errorf("Content-Length = %q, want cleared (stale value from before the error, doesn't match the actual body of %d bytes)", got, w.Body.Len())
			}
			if got := w.Header().Get("Trailer"); got != "" {
				t.Errorf("Trailer = %q, want cleared (no trailers are actually sent)", got)
			}
			if got := w.Header().Get("Content-Encoding"); got != "" {
				t.Errorf("Content-Encoding = %q, want cleared (the body is always plain, uncompressed text)", got)
			}
			if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
		})
	}
}

func TestWriteJSONFallsBackOnUnencodableDetail(t *testing.T) {
	// SetPublicDetail accepts an arbitrary value; if it can't be
	// JSON-encoded, WriteJSON must not silently commit a status claiming
	// success with an empty or broken body.
	w := httptest.NewRecorder()
	err := New(ErrCodeInvalidInput, "invalid")
	err.SetPublicDetail("bad", make(chan int))

	status := WriteJSON(w, err)

	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d (the fallback status, not the original error's %d)", status, http.StatusInternalServerError, http.StatusBadRequest)
	}
	if w.Code != status {
		t.Errorf("w.Code = %d, want it to match the returned status %d", w.Code, status)
	}

	var resp HTTPErrorResponse
	if decErr := json.Unmarshal(w.Body.Bytes(), &resp); decErr != nil {
		t.Fatalf("body is not valid JSON: %v (body: %q)", decErr, w.Body.String())
	}
	if resp.Error.Code != ErrCodeInternal {
		t.Errorf("Error.Code = %v, want %v", resp.Error.Code, ErrCodeInternal)
	}
}

func TestWriteHTTPErrorLogsRenderFailureOnUnencodableDetail(t *testing.T) {
	// The client-visible response falls back to a generic INTERNAL_ERROR
	// (TestWriteJSONFallsBackOnUnencodableDetail), but that alone gives no
	// way to diagnose why - the log line must say a marshal failure
	// happened and what the client actually received differed from the
	// error's own classification.
	w := httptest.NewRecorder()
	logger := &recordingLogger{}
	err := NewValidationError("bad input", "name", nil)
	err.SetPublicDetail("bad", make(chan int))

	WriteHTTPError(w, err, logger)

	if len(logger.calls) != 1 {
		t.Fatalf("logger.calls = %d, want 1", len(logger.calls))
	}
	fields := logger.calls[0].fields
	if fields["error_code"] != string(ErrCodeInvalidInput) {
		t.Errorf(`fields["error_code"] = %v, want %q (the error's own, original classification)`, fields["error_code"], ErrCodeInvalidInput)
	}
	if fields["rendered_error_code"] != string(ErrCodeInternal) {
		t.Errorf(`fields["rendered_error_code"] = %v, want %q (what the client actually received)`, fields["rendered_error_code"], ErrCodeInternal)
	}
	renderErr, ok := fields["response_render_error"].(string)
	if !ok || renderErr == "" {
		t.Errorf(`fields["response_render_error"] = %v, want a non-empty marshal error message`, fields["response_render_error"])
	}
}

func TestWriteHTTPProblemLogsRenderFailureOnUnencodableDetail(t *testing.T) {
	w := httptest.NewRecorder()
	logger := &recordingLogger{}
	err := New(ErrCodeInvalidInput, "invalid")
	err.SetPublicDetail("bad", make(chan int))

	WriteHTTPProblem(w, err, logger)

	if len(logger.calls) != 1 {
		t.Fatalf("logger.calls = %d, want 1", len(logger.calls))
	}
	fields := logger.calls[0].fields
	if fields["rendered_error_code"] != string(ErrCodeInternal) {
		t.Errorf(`fields["rendered_error_code"] = %v, want %q`, fields["rendered_error_code"], ErrCodeInternal)
	}
	if _, ok := fields["response_render_error"]; !ok {
		t.Error(`fields["response_render_error"] missing, want the marshal error message`)
	}
}

func TestWriteHTTPErrorDoesNotLogRenderFailureOnSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	logger := &recordingLogger{}

	WriteHTTPError(w, NewNotFoundError("league", "1"), logger)

	fields := logger.calls[0].fields
	if _, ok := fields["response_render_error"]; ok {
		t.Errorf(`fields["response_render_error"] = %v, want the key entirely absent when marshaling succeeded`, fields["response_render_error"])
	}
	if _, ok := fields["rendered_error_code"]; ok {
		t.Errorf(`fields["rendered_error_code"] = %v, want the key entirely absent when marshaling succeeded`, fields["rendered_error_code"])
	}
}

func TestWriteProblemFallsBackOnUnencodableDetail(t *testing.T) {
	w := httptest.NewRecorder()
	err := New(ErrCodeInvalidInput, "invalid")
	err.SetPublicDetail("bad", make(chan int))

	status := WriteProblem(w, err)

	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", status, http.StatusInternalServerError)
	}

	var resp map[string]interface{}
	if decErr := json.Unmarshal(w.Body.Bytes(), &resp); decErr != nil {
		t.Fatalf("body is not valid JSON: %v (body: %q)", decErr, w.Body.String())
	}
	if resp["code"] != string(ErrCodeInternal) {
		t.Errorf(`resp["code"] = %v, want %q`, resp["code"], ErrCodeInternal)
	}
	if resp["status"] != float64(http.StatusInternalServerError) {
		t.Errorf(`resp["status"] = %v, want %d`, resp["status"], http.StatusInternalServerError)
	}
}

func TestWriteResultFunctionsMirrorTheirIntCounterparts(t *testing.T) {
	err := NewNotFoundError("league", "1")

	t.Run("WriteJSONResult", func(t *testing.T) {
		w := httptest.NewRecorder()
		got := WriteJSONResult(w, err)
		if got.Status != http.StatusNotFound {
			t.Errorf("Status = %d, want %d", got.Status, http.StatusNotFound)
		}
		if got.RenderErr != nil || got.WriteErr != nil {
			t.Errorf("RenderErr/WriteErr = %v/%v, want nil/nil on a normal write", got.RenderErr, got.WriteErr)
		}

		other := httptest.NewRecorder()
		if wantStatus := WriteJSON(other, err); got.Status != wantStatus || w.Body.String() != other.Body.String() {
			t.Errorf("WriteJSONResult diverged from WriteJSON: status %d vs %d, body %q vs %q", got.Status, wantStatus, w.Body.String(), other.Body.String())
		}
	})

	t.Run("WriteHTMLResult", func(t *testing.T) {
		w := httptest.NewRecorder()
		got := WriteHTMLResult(w, err)
		if got.Status != http.StatusNotFound {
			t.Errorf("Status = %d, want %d", got.Status, http.StatusNotFound)
		}
		if got.RenderErr != nil {
			t.Error("RenderErr should always be nil for HTML - the body is plain string concatenation, not JSON")
		}
		if got.WriteErr != nil {
			t.Errorf("WriteErr = %v, want nil on a normal write", got.WriteErr)
		}
	})

	t.Run("WriteProblemResult", func(t *testing.T) {
		w := httptest.NewRecorder()
		got := WriteProblemResult(w, err)
		if got.Status != http.StatusNotFound {
			t.Errorf("Status = %d, want %d", got.Status, http.StatusNotFound)
		}
		if got.RenderErr != nil || got.WriteErr != nil {
			t.Errorf("RenderErr/WriteErr = %v/%v, want nil/nil on a normal write", got.RenderErr, got.WriteErr)
		}
	})

	t.Run("WriteJSONResult reports a render failure", func(t *testing.T) {
		w := httptest.NewRecorder()
		bad := New(ErrCodeInvalidInput, "invalid")
		bad.SetPublicDetail("bad", make(chan int))

		got := WriteJSONResult(w, bad)
		if got.Status != http.StatusInternalServerError {
			t.Errorf("Status = %d, want %d", got.Status, http.StatusInternalServerError)
		}
		if got.RenderErr == nil {
			t.Error("RenderErr = nil, want the marshal error - a caller using the Result API should be able to detect this without a Logger")
		}
	})

	t.Run("WriteJSONResult reports a write failure", func(t *testing.T) {
		w := &failingWriter{}
		got := WriteJSONResult(w, err)
		if got.WriteErr == nil {
			t.Error("WriteErr = nil, want the write failure")
		}
	})
}

func TestSafeLogContainsAPanickingLogger(t *testing.T) {
	// safeLog must not let a broken Logger's own panic escape - inside
	// RecoveryMiddleware, that panic would propagate out of svcerr's
	// already-executing recover(), uncaught, replacing the original
	// panic's diagnostics with a generic trace pointing at the logger.
	panicking := loggerFunc(func(Level, error, map[string]interface{}, string) {
		panic("logger is broken")
	})

	didPanic := func() (panicked bool) {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		safeLog(panicking, LevelError, nil, nil, "test")
		return false
	}()

	if didPanic {
		t.Error("safeLog let the logger's own panic escape, want it contained")
	}
}

func TestRecoveryMiddlewareSurvivesAPanickingLogger(t *testing.T) {
	// End-to-end: a Logger that panics must not prevent RecoveryMiddleware
	// from still turning the handler's original panic into a proper error
	// response - the whole point of the middleware.
	panicking := loggerFunc(func(Level, error, map[string]interface{}, string) {
		panic("logger is broken")
	})
	handler := RecoveryMiddleware(panicking)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("original bug")
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d (a panicking logger must not prevent the error response from being written)", w.Code, http.StatusInternalServerError)
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
	wrapped, tw := newTrackingResponseWriter(rec)

	f, ok := wrapped.(http.Flusher)
	if !ok {
		t.Fatal("wrapped should implement http.Flusher (the underlying recorder does)")
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
// http.Flusher or http.Hijacker - to verify newTrackingResponseWriter
// doesn't advertise capabilities the underlying writer doesn't have.
type nonFlushingWriter struct {
	rec *httptest.ResponseRecorder
}

func (w *nonFlushingWriter) Header() http.Header         { return w.rec.Header() }
func (w *nonFlushingWriter) Write(b []byte) (int, error) { return w.rec.Write(b) }
func (w *nonFlushingWriter) WriteHeader(statusCode int)  { w.rec.WriteHeader(statusCode) }

// hijackOnlyWriter implements http.ResponseWriter and http.Hijacker but
// deliberately not http.Flusher, to exercise
// newTrackingResponseWriter's hijack-only dispatch path.
type hijackOnlyWriter struct {
	*nonFlushingWriter
	hijacked bool
}

func (w *hijackOnlyWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	server, _ := net.Pipe()
	return server, bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)), nil
}

func TestNewTrackingResponseWriterPreservesCapabilities(t *testing.T) {
	t.Run("neither Flusher nor Hijacker", func(t *testing.T) {
		underlying := &nonFlushingWriter{rec: httptest.NewRecorder()}
		wrapped, _ := newTrackingResponseWriter(underlying)

		if _, ok := wrapped.(http.Flusher); ok {
			t.Error("wrapped implements http.Flusher, want it not to - the underlying writer doesn't")
		}
		if _, ok := wrapped.(http.Hijacker); ok {
			t.Error("wrapped implements http.Hijacker, want it not to - the underlying writer doesn't")
		}
	})

	t.Run("Flusher only", func(t *testing.T) {
		underlying := httptest.NewRecorder()
		wrapped, _ := newTrackingResponseWriter(underlying)

		if _, ok := wrapped.(http.Flusher); !ok {
			t.Error("wrapped does not implement http.Flusher, want it to - the underlying writer does")
		}
		if _, ok := wrapped.(http.Hijacker); ok {
			t.Error("wrapped implements http.Hijacker, want it not to - the underlying writer doesn't")
		}
	})

	t.Run("Hijacker only", func(t *testing.T) {
		underlying := &hijackOnlyWriter{nonFlushingWriter: &nonFlushingWriter{rec: httptest.NewRecorder()}}
		wrapped, tw := newTrackingResponseWriter(underlying)

		if _, ok := wrapped.(http.Flusher); ok {
			t.Error("wrapped implements http.Flusher, want it not to - the underlying writer doesn't")
		}
		hj, ok := wrapped.(http.Hijacker)
		if !ok {
			t.Fatal("wrapped does not implement http.Hijacker, want it to - the underlying writer does")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("Hijack() error = %v", err)
		}
		defer func() { _ = conn.Close() }()
		if !underlying.hijacked {
			t.Error("Hijack() did not delegate to the underlying writer")
		}
		if !tw.wroteHeader {
			t.Error("a successful Hijack() should mark the response committed")
		}
	})

	t.Run("both Flusher and Hijacker", func(t *testing.T) {
		underlying := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
		wrapped, tw := newTrackingResponseWriter(underlying)

		f, ok := wrapped.(http.Flusher)
		if !ok {
			t.Fatal("wrapped does not implement http.Flusher, want it to - the underlying writer does")
		}
		if _, ok := wrapped.(http.Hijacker); !ok {
			t.Error("wrapped does not implement http.Hijacker, want it to - the underlying writer does")
		}
		f.Flush()
		if !underlying.Flushed {
			t.Error("Flush() did not delegate to the underlying writer")
		}
		if !tw.wroteHeader {
			t.Error("Flush() should mark the response committed")
		}
	})
}

func TestResponseControllerReportsUnsupportedCapabilitiesCorrectly(t *testing.T) {
	// http.ResponseController is documented to return an error when the
	// underlying writer doesn't support the requested operation - which
	// only works if this package's wrapper doesn't itself falsely claim
	// the capability (it would otherwise intercept the call before
	// ResponseController's Unwrap traversal ever reaches the real
	// underlying writer).
	underlying := &nonFlushingWriter{rec: httptest.NewRecorder()}
	wrapped, _ := newTrackingResponseWriter(underlying)
	controller := http.NewResponseController(wrapped)

	if err := controller.Flush(); err == nil {
		t.Error("ResponseController.Flush() error = nil, want an error since the underlying writer doesn't support http.Flusher")
	}
	if _, _, err := controller.Hijack(); err == nil {
		t.Error("ResponseController.Hijack() error = nil, want an error since the underlying writer doesn't support http.Hijacker")
	}
}

// flushErrorWriter implements http.Flusher and the optional FlushError()
// error method http.ResponseController prefers over it - to verify
// newTrackingResponseWriter selects a variant that preserves FlushError
// instead of one that only forwards to plain Flush() and silently
// discards the error.
type flushErrorWriter struct {
	*httptest.ResponseRecorder
	err error
}

func (w *flushErrorWriter) FlushError() error { return w.err }

// flushErrorOnlyWriter implements http.ResponseWriter and FlushError()
// error but deliberately NOT plain Flush() - legal, since
// http.ResponseController documents the two as alternatives.
type flushErrorOnlyWriter struct {
	rec      *httptest.ResponseRecorder
	flushErr error
	flushed  bool
}

func (w *flushErrorOnlyWriter) Header() http.Header         { return w.rec.Header() }
func (w *flushErrorOnlyWriter) Write(b []byte) (int, error) { return w.rec.Write(b) }
func (w *flushErrorOnlyWriter) WriteHeader(code int)        { w.rec.WriteHeader(code) }
func (w *flushErrorOnlyWriter) FlushError() error {
	w.flushed = true
	return w.flushErr
}

func TestFlushErrorOnlyWriterGainsFlusherDeliberately(t *testing.T) {
	// Documented asymmetry (see newTrackingResponseWriter): a writer with
	// only FlushError() gains a plain Flush() method through the wrapper,
	// because the flush capability genuinely exists underneath and
	// http.Flusher is how handlers conventionally probe for it. This test
	// pins that as deliberate - if strict capability-matching is ever
	// wanted instead, this is the behavior being traded away.
	underlying := &flushErrorOnlyWriter{rec: httptest.NewRecorder()}
	wrapped, tw := newTrackingResponseWriter(underlying)

	f, ok := wrapped.(http.Flusher)
	if !ok {
		t.Fatal("wrapped does not implement http.Flusher, want it to (deliberate adapter over the underlying FlushError capability)")
	}

	f.Flush()
	if !underlying.flushed {
		t.Error("Flush() did not delegate to the underlying FlushError()")
	}
	if !tw.wroteHeader {
		t.Error("a successful flush through the adapter should mark the response committed")
	}

	// The richer form is preserved too - no error information is lost.
	if _, ok := wrapped.(interface{ FlushError() error }); !ok {
		t.Error("wrapped does not implement FlushError() error, want it preserved alongside the Flush() adapter")
	}
}

func TestResponseControllerPreservesFlushError(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		underlying := &flushErrorWriter{ResponseRecorder: httptest.NewRecorder(), err: errors.New("flush failed")}
		wrapped, tw := newTrackingResponseWriter(underlying)

		err := http.NewResponseController(wrapped).Flush()
		if err == nil || err.Error() != "flush failed" {
			t.Errorf("ResponseController.Flush() error = %v, want the underlying FlushError() error to be preserved, not shadowed by a plain http.Flusher passthrough", err)
		}
		// Matches real net/http: WriteHeader(200) commits before the flush
		// is even attempted, so a failure can happen after the status line
		// is already on the wire - marking committed only on success would
		// leave RecoveryMiddleware believing a fresh response is still safe
		// to write.
		if !tw.wroteHeader {
			t.Error("a failed FlushError() should still mark the response committed, the same way a real net/http FlushError commits before attempting the flush")
		}
	})

	t.Run("success", func(t *testing.T) {
		underlying := &flushErrorWriter{ResponseRecorder: httptest.NewRecorder(), err: nil}
		wrapped, tw := newTrackingResponseWriter(underlying)

		if err := http.NewResponseController(wrapped).Flush(); err != nil {
			t.Errorf("ResponseController.Flush() error = %v, want nil", err)
		}
		if !tw.wroteHeader {
			t.Error("a successful FlushError() should mark the response committed, the same way a plain successful Flush() does")
		}
	})

	t.Run("plain Flush() still discards the error but still commits and delegates through FlushError", func(t *testing.T) {
		underlying := &flushErrorWriter{ResponseRecorder: httptest.NewRecorder(), err: errors.New("flush failed")}
		wrapped, tw := newTrackingResponseWriter(underlying)

		wrapped.(http.Flusher).Flush()

		if !tw.wroteHeader {
			t.Error("Flush() delegating to a failing FlushError() should still mark the response committed, even though http.Flusher's signature can't report the failure to the caller")
		}
	})

	t.Run("combined with Hijacker", func(t *testing.T) {
		hijackable := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
		underlying := struct {
			*flushErrorWriter
			http.Hijacker
		}{
			flushErrorWriter: &flushErrorWriter{ResponseRecorder: httptest.NewRecorder(), err: nil},
			Hijacker:         hijackable,
		}
		wrapped, tw := newTrackingResponseWriter(underlying)

		fe, ok := wrapped.(interface{ FlushError() error })
		if !ok {
			t.Fatal("wrapped does not implement FlushError() error, want it to - the underlying writer does")
		}
		hj, ok := wrapped.(http.Hijacker)
		if !ok {
			t.Fatal("wrapped does not implement http.Hijacker, want it to - the underlying writer does")
		}

		wrapped.(http.Flusher).Flush()
		if !tw.wroteHeader {
			t.Error("Flush() should mark the response committed on a successful underlying FlushError()")
		}

		if err := fe.FlushError(); err != nil {
			t.Errorf("FlushError() = %v, want nil", err)
		}

		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("Hijack() error = %v", err)
		}
		defer func() { _ = conn.Close() }()
		if !hijackable.hijacked {
			t.Error("Hijack() did not delegate to the underlying writer")
		}
	})
}

// unwrapOnly wraps a ResponseWriter with nothing but http.ResponseWriter
// and Unwrap() http.ResponseWriter - no Flush, FlushError, or Hijack of its
// own. This is the shape http.ResponseController documents as how a
// middleware wrapper preserves controller operations through itself: a
// legitimate, commonly-used pattern, not a contrived type. Used to verify
// newTrackingResponseWriter's capability discovery follows the same Unwrap
// chain http.ResponseController itself follows (assessment 0008/L1),
// instead of only checking the immediate underlying writer.
type unwrapOnly struct{ http.ResponseWriter }

func (w *unwrapOnly) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func TestNewTrackingResponseWriterDiscoversCapabilitiesThroughUnwrapChain(t *testing.T) {
	t.Run("Flusher behind an Unwrap-only wrapper is discovered and tracked", func(t *testing.T) {
		rec := httptest.NewRecorder()
		underlying := &unwrapOnly{ResponseWriter: rec}
		wrapped, tw := newTrackingResponseWriter(underlying)

		// Before the fix, ResponseController.Flush() on `wrapped` would
		// unwrap straight past this tracker (via trackingResponseWriter's
		// own Unwrap()) to rec's real Flush(), without tw ever being
		// marked committed - the exact bypass assessment 0008/L1
		// describes. Asserting through http.ResponseController here,
		// rather than a direct wrapped.(http.Flusher) check, is what
		// actually exercises that code path.
		if err := http.NewResponseController(wrapped).Flush(); err != nil {
			t.Fatalf("ResponseController.Flush() error = %v, want nil", err)
		}
		if !rec.Flushed {
			t.Error("Flush() did not reach the real underlying recorder through the Unwrap-only layer")
		}
		if !tw.wroteHeader {
			t.Error("tw.wroteHeader = false after a flush reached through an Unwrap-only wrapper, want true - this is the commit-tracking bypass assessment 0008/L1 describes")
		}
	})

	t.Run("FlushError behind an Unwrap-only wrapper is discovered and preserved", func(t *testing.T) {
		underlying := &unwrapOnly{ResponseWriter: &flushErrorWriter{ResponseRecorder: httptest.NewRecorder(), err: errors.New("flush failed")}}
		wrapped, tw := newTrackingResponseWriter(underlying)

		err := http.NewResponseController(wrapped).Flush()
		if err == nil || err.Error() != "flush failed" {
			t.Errorf("ResponseController.Flush() error = %v, want the FlushError() error reached through the Unwrap-only layer to be preserved", err)
		}
		if !tw.wroteHeader {
			t.Error("tw.wroteHeader = false after a FlushError reached through an Unwrap-only wrapper, want true")
		}
	})

	t.Run("Hijacker behind an Unwrap-only wrapper is discovered and tracked", func(t *testing.T) {
		hijackable := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
		underlying := &unwrapOnly{ResponseWriter: hijackable}
		wrapped, tw := newTrackingResponseWriter(underlying)

		conn, _, err := http.NewResponseController(wrapped).Hijack()
		if err != nil {
			t.Fatalf("ResponseController.Hijack() error = %v, want nil", err)
		}
		defer func() { _ = conn.Close() }()
		if !hijackable.hijacked {
			t.Error("Hijack() did not reach the real underlying recorder through the Unwrap-only layer")
		}
		if !tw.wroteHeader {
			t.Error("tw.wroteHeader = false after a hijack reached through an Unwrap-only wrapper, want true")
		}
	})

	t.Run("a plain Flusher one layer down does not shadow a FlushError two layers down", func(t *testing.T) {
		// http.ResponseController checks FlushError ahead of Flusher at
		// each layer, then descends - so a plain Flusher at a shallower
		// layer wins over a FlushError at a deeper one, since the
		// traversal never reaches the deeper layer. discoverFlusher must
		// reproduce that same per-layer priority, not "search every layer
		// for FlushError first, then every layer for Flusher".
		inner := &flushErrorWriter{ResponseRecorder: httptest.NewRecorder(), err: errors.New("should not be reached")}
		shallow := &shallowFlusher{inner: inner}
		wrapped, tw := newTrackingResponseWriter(&unwrapOnly{ResponseWriter: shallow})

		if _, ok := wrapped.(interface{ FlushError() error }); ok {
			t.Error("wrapped implements FlushError() error, want it not to - the shallower plain Flusher must shadow the deeper FlushError, matching http.ResponseController's own per-layer priority")
		}
		f, ok := wrapped.(http.Flusher)
		if !ok {
			t.Fatal("wrapped does not implement http.Flusher, want it to")
		}
		f.Flush()
		if !tw.wroteHeader {
			t.Error("Flush() should still mark the response committed")
		}
		if !shallow.flushed {
			t.Error("the shallower plain Flush() was not called")
		}
	})
}

// shallowFlusher implements only plain http.Flusher (no FlushError), one
// layer above a writer (inner) that implements FlushError - used to verify
// discoverFlusher stops at the first layer with either capability instead
// of preferring a deeper FlushError over a shallower plain Flusher.
type shallowFlusher struct {
	inner   *flushErrorWriter
	flushed bool
}

func (w *shallowFlusher) Header() http.Header         { return w.inner.Header() }
func (w *shallowFlusher) Write(b []byte) (int, error) { return w.inner.Write(b) }
func (w *shallowFlusher) WriteHeader(code int)        { w.inner.WriteHeader(code) }
func (w *shallowFlusher) Flush()                      { w.flushed = true }

func TestRecoveryMiddlewareAbortsOnPanicAfterFlushThroughUnwrapOnlyWrapper(t *testing.T) {
	// Reproduction of assessment 0008/L1, mirroring the existing
	// "response committed via Flush without WriteHeader is not appended
	// to" case above but with an Unwrap-only wrapper sitting BETWEEN the
	// real writer and RecoveryMiddleware's own newTrackingResponseWriter
	// call. Before the fix, newTrackingResponseWriter only checked the
	// immediate writer (the unwrapOnly value, which has no Flusher of its
	// own) for capabilities, so http.ResponseController.Flush() unwrapped
	// straight past this package's tracker to the real recorder - flushing
	// (and thereby committing, per net/http's own semantics) it without
	// tw.wroteHeader ever being set. RecoveryMiddleware then took the
	// uncommitted branch and appended a second, corrupting response on top
	// of the first - the same externally-misleading result K1 (assessment
	// 0007) already fixed for the direct-Flusher case.
	logger := &recordingLogger{}
	rec := httptest.NewRecorder()
	handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("Flush() through the Unwrap-only wrapper failed unexpectedly: %v", err)
		}
		panic("boom after flush via an Unwrap-only wrapper")
	}))

	expectAbortHandler(t, handler, &unwrapOnly{ResponseWriter: rec}, httptest.NewRequest(http.MethodGet, "/", nil))

	if !rec.Flushed {
		t.Error("the real underlying recorder was never flushed - the Unwrap-only wrapper didn't reach it")
	}
	if got := rec.Body.String(); got != "" {
		t.Errorf("body = %q, want empty (Flush commits the response through the Unwrap-only wrapper, so no error JSON should be appended on top of it)", got)
	}

	if len(logger.calls) != 1 {
		t.Fatalf("logger.calls = %d, want 1, got %+v", len(logger.calls), logger.calls)
	}
	if logger.calls[0].msg != "Panic recovered in HTTP handler after response was already committed" {
		t.Errorf("log msg = %q, want the committed-response variant", logger.calls[0].msg)
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

// panicOnInvalidStatusWriter mimics net/http's own WriteHeader validation
// (net/http.checkWriteHeaderCode): it panics before writing anything if
// status is outside the accepted three-digit range, the same way a real
// connection does. Used to verify trackingResponseWriter doesn't record
// commitment for a WriteHeader call that never actually reached the
// connection.
type panicOnInvalidStatusWriter struct {
	*httptest.ResponseRecorder
}

func (w *panicOnInvalidStatusWriter) WriteHeader(status int) {
	if status < 100 || status > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", status))
	}
	w.ResponseRecorder.WriteHeader(status)
}

func TestTrackingResponseWriterDoesNotRecordCommitmentWhenTheDelegateWriteHeaderPanics(t *testing.T) {
	// Assessment 0008/L3: trackingResponseWriter.WriteHeader used to set
	// wroteHeader/status BEFORE delegating, so an invalid status that made
	// the real underlying WriteHeader panic (before anything reached the
	// connection) was still falsely recorded as committed.
	underlying := &panicOnInvalidStatusWriter{ResponseRecorder: httptest.NewRecorder()}
	tw := &trackingResponseWriter{ResponseWriter: underlying}

	func() {
		defer func() { _ = recover() }()
		tw.WriteHeader(99)
	}()

	if tw.wroteHeader {
		t.Error("tw.wroteHeader = true after the delegate WriteHeader panicked on an invalid status, want false - nothing actually reached the connection")
	}
}

func TestRecoveryMiddlewareWritesARealErrorResponseAfterAnInvalidStatusPanic(t *testing.T) {
	// End-to-end companion to the direct trackingResponseWriter test above:
	// before the fix, RecoveryMiddleware's deferred function saw
	// tw.wroteHeader falsely set to true and took the already-committed
	// branch (log, then abort the connection with http.ErrAbortHandler)
	// instead of writing the real, valid 500 response that was still safe
	// to send, since nothing had actually reached the connection yet.
	logger := &recordingLogger{}
	underlying := &panicOnInvalidStatusWriter{ResponseRecorder: httptest.NewRecorder()}
	handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(99) // invalid; a real net/http writer panics here before anything is sent
	}))

	handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))

	if underlying.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d (a real error response - nothing had actually reached the connection before the panic)", underlying.Code, http.StatusInternalServerError)
	}
	var resp HTTPErrorResponse
	if jsonErr := json.Unmarshal(underlying.Body.Bytes(), &resp); jsonErr != nil {
		t.Fatalf("body is not valid JSON: %v (body: %s)", jsonErr, underlying.Body.String())
	}
	if resp.Error.Code != ErrCodeInternal {
		t.Errorf("Error.Code = %v, want %v", resp.Error.Code, ErrCodeInternal)
	}
	if len(logger.calls) != 1 {
		t.Fatalf("logger.calls = %d, want 1, got %+v", len(logger.calls), logger.calls)
	}
	if logger.calls[0].msg != "Panic recovered in HTTP handler" {
		t.Errorf("log msg = %q, want the normal (uncommitted) variant, not the already-committed one", logger.calls[0].msg)
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
		wrapped, _ := newTrackingResponseWriter(rec)

		if _, ok := wrapped.(http.Hijacker); ok {
			t.Fatal("wrapped implements http.Hijacker, want it not to - httptest.ResponseRecorder doesn't support hijacking")
		}
	})

	t.Run("successful hijack delegates and marks the response committed", func(t *testing.T) {
		underlying := &hijackableRecorder{ResponseRecorder: httptest.NewRecorder()}
		wrapped, tw := newTrackingResponseWriter(underlying)

		hj, ok := wrapped.(http.Hijacker)
		if !ok {
			t.Fatal("wrapped should implement http.Hijacker (the underlying recorder does)")
		}
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

// expectAbortHandler runs handler via a real ServeHTTP call, asserting it
// panics with http.ErrAbortHandler instead of returning normally - the
// signal RecoveryMiddleware re-panics with after logging a panic it can no
// longer safely turn into a fresh response body (the response was already
// committed, or the replacement body it wrote failed to fully deliver).
// Recovers that panic itself so it doesn't escape the test goroutine.
func expectAbortHandler(t *testing.T, handler http.Handler, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	defer func() {
		rec := recover()
		if rec != http.ErrAbortHandler {
			t.Errorf("recovered %v, want it to re-panic with http.ErrAbortHandler", rec)
		}
	}()
	handler.ServeHTTP(w, r)
	t.Error("ServeHTTP returned normally, want it to panic with http.ErrAbortHandler")
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

	t.Run("recovers an abnormal exit that leaves recover() reporting nil", func(t *testing.T) {
		// recover() reports nil both for a genuinely uneventful request and
		// for a handler that called panic(nil) under the pre-Go 1.21
		// panicnil GODEBUG default - which Go selects from the *main*
		// module's go directive, not this package's, so it isn't under
		// this test binary's control. runtime.Goexit produces the same
		// "recover() is nil" observation deterministically, regardless of
		// GODEBUG state, so it stands in here for both that case and for
		// panic(nil) itself: RecoveryMiddleware must not mistake either
		// for normal completion.
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			runtime.Goexit()
		}))

		w := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		}()
		<-done

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
		}
		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1, got %+v", len(logger.calls), logger.calls)
		}
		if logger.calls[0].fields["error_code"] != string(ErrCodeInternal) {
			t.Errorf("error_code field = %v, want %v", logger.calls[0].fields["error_code"], ErrCodeInternal)
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
		expectAbortHandler(t, handler, w, httptest.NewRequest(http.MethodGet, "/", nil))

		// The 200 already sent to the client can't be retracted - the
		// recorder keeps whatever status was written first. The abort
		// (rather than a normal return) is what stops net/http from then
		// treating this truncated body as a complete, successful response.
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
		expectAbortHandler(t, handler, w, httptest.NewRequest(http.MethodGet, "/", nil))

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
		expectAbortHandler(t, handler, w, httptest.NewRequest(http.MethodGet, "/", nil))

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

	t.Run("checked Flusher assertion correctly reports unsupported on a non-flushing writer", func(t *testing.T) {
		logger := &recordingLogger{}
		underlying := &nonFlushingWriter{rec: httptest.NewRecorder()}
		flushed := false
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if f, ok := w.(http.Flusher); ok {
				flushed = true
				f.Flush()
			}
			w.WriteHeader(http.StatusNoContent)
		}))

		handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))

		if flushed {
			t.Error("handler's w.(http.Flusher) assertion succeeded, want it to fail - the underlying writer doesn't support Flusher")
		}
		if underlying.rec.Code != http.StatusNoContent {
			t.Errorf("status = %d, want %d", underlying.rec.Code, http.StatusNoContent)
		}
		if len(logger.calls) != 0 {
			t.Errorf("logger.calls = %d, want 0 (no panic occurred)", len(logger.calls))
		}
	})

	t.Run("a handler's own unchecked Flusher assertion panic is still recovered normally", func(t *testing.T) {
		logger := &recordingLogger{}
		underlying := &nonFlushingWriter{rec: httptest.NewRecorder()}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// A careless handler that skips the ok check now gets a
			// runtime type-assertion panic here, since the underlying
			// writer doesn't support http.Flusher and the wrapper
			// correctly doesn't either - rather than the old behavior of
			// silently no-op'ing. RecoveryMiddleware must still recover
			// this like any other panic.
			w.(http.Flusher).Flush()
		}))

		handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))

		if underlying.rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", underlying.rec.Code, http.StatusInternalServerError)
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
		// connection, has no special handling for a 1xx WriteHeader call
		// followed by a final one - it just latches its Code/wroteHeader
		// on the first call regardless of status and then refuses the
		// later body as "not allowed for this status" (103 doesn't permit
		// one), which would make the real fix (aborting on a write
		// failure) fire here on a test-double artifact that can't happen
		// against a real connection. Run this one against a real server
		// instead, so 1xx is handled the way net/http itself handles it.
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusEarlyHints) // 103 - not the final response
			panic("boom after an informational header")
		}))

		server := httptest.NewServer(handler)
		defer server.Close()

		resp, err := http.Get(server.URL)
		if err != nil {
			t.Fatalf("http.Get() error = %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
		}
		var errResp HTTPErrorResponse
		if jsonErr := json.Unmarshal(body, &errResp); jsonErr != nil {
			t.Fatalf("body is not valid JSON: %v (body: %s)", jsonErr, body)
		}
		if errResp.Error.Code != ErrCodeInternal {
			t.Errorf("Error.Code = %v, want %v", errResp.Error.Code, ErrCodeInternal)
		}

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

		expectAbortHandler(t, handler, underlying, httptest.NewRequest(http.MethodGet, "/", nil))

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

	t.Run("checked Hijacker assertion correctly reports unsupported on a non-hijacking writer", func(t *testing.T) {
		logger := &recordingLogger{}
		underlying := &nonFlushingWriter{rec: httptest.NewRecorder()}
		hijacked := false
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hj, ok := w.(http.Hijacker); ok {
				hijacked = true
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			}
			w.WriteHeader(http.StatusNoContent)
		}))

		handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))

		if hijacked {
			t.Error("handler's w.(http.Hijacker) assertion succeeded, want it to fail - the underlying writer doesn't support Hijacker")
		}
		if underlying.rec.Code != http.StatusNoContent {
			t.Errorf("status = %d, want %d", underlying.rec.Code, http.StatusNoContent)
		}
		if len(logger.calls) != 0 {
			t.Errorf("logger.calls = %d, want 0 (no panic occurred)", len(logger.calls))
		}
	})

	t.Run("a handler's own unchecked Hijacker assertion panic is still recovered normally", func(t *testing.T) {
		logger := &recordingLogger{}
		underlying := &nonFlushingWriter{rec: httptest.NewRecorder()}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// A careless handler that skips the ok check now gets a
			// runtime type-assertion panic here, since the underlying
			// writer doesn't support http.Hijacker and the wrapper
			// correctly doesn't either - rather than the old behavior of
			// returning a synthetic "does not implement http.Hijacker"
			// error from Hijack() itself. RecoveryMiddleware must still
			// recover this like any other panic.
			_, _, _ = w.(http.Hijacker).Hijack()
		}))

		handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/", nil))

		if underlying.rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", underlying.rec.Code, http.StatusInternalServerError)
		}
		var resp HTTPErrorResponse
		if err := json.Unmarshal(underlying.rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("body is not valid JSON: %v (body: %s)", err, underlying.rec.Body.String())
		}
		if resp.Error.Code != ErrCodeInternal {
			t.Errorf("Error.Code = %v, want %v", resp.Error.Code, ErrCodeInternal)
		}
	})

	t.Run("failure writing the replacement body also aborts instead of returning normally", func(t *testing.T) {
		// Not-yet-committed panic path, but the replacement JSON error
		// body itself fails to write (client disconnect, expired
		// deadline, ...) - the client may have received a partial,
		// invalid document, the same "looks like success, isn't" problem
		// as panicking after the response was already committed.
		logger := &recordingLogger{}
		handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("boom")
		}))

		w := &failingWriter{}
		expectAbortHandler(t, handler, w, httptest.NewRequest(http.MethodGet, "/", nil))

		if len(logger.calls) != 1 {
			t.Fatalf("logger.calls = %d, want 1, got %+v", len(logger.calls), logger.calls)
		}
		if logger.calls[0].msg != "Panic recovered in HTTP handler" {
			t.Errorf("log msg = %q, want the not-yet-committed variant (the panic itself wasn't after a commit - only the replacement write failed)", logger.calls[0].msg)
		}
		if _, ok := logger.calls[0].fields["response_write_error"]; !ok {
			t.Error(`fields["response_write_error"] missing, want the write failure recorded before the abort`)
		}
	})
}
