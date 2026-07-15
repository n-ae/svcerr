package errors

import (
	"encoding/json"
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
		{ErrCodeDatabaseQuery, http.StatusServiceUnavailable},
		{ErrCodeDatabaseTransaction, http.StatusServiceUnavailable},
		{ErrCodeDatabaseMigration, http.StatusServiceUnavailable},
		{ErrCodeInternal, http.StatusInternalServerError},
		{ErrCodeNotImplemented, http.StatusInternalServerError},
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

	t.Run("rate limit error wrapped still sets Retry-After header", func(t *testing.T) {
		w := httptest.NewRecorder()
		logger := &recordingLogger{}
		inner := NewRateLimitError("yahoo", 300, 60)
		wrapped := WrapInternalError(inner, "handler", "propagated")

		WriteHTTPError(w, wrapped, logger)

		if got := w.Header().Get("Retry-After"); got != "60" {
			t.Errorf("Retry-After = %q, want 60 (should unwrap via errors.As)", got)
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
// "unknown error" fallback path independent of the errors package's own
// New()/errors.New value type.
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

		// One call from RecoveryMiddleware's own "Panic recovered" log,
		// one from WriteHTTPError's "HTTP error response" log.
		if len(logger.calls) != 2 {
			t.Fatalf("logger.calls = %d, want 2, got %+v", len(logger.calls), logger.calls)
		}
		if logger.calls[0].msg != "Panic recovered in HTTP handler" {
			t.Errorf("first log msg = %q, want %q", logger.calls[0].msg, "Panic recovered in HTTP handler")
		}
		if logger.calls[0].fields["method"] != http.MethodGet {
			t.Errorf("method field = %v, want GET", logger.calls[0].fields["method"])
		}
		if logger.calls[0].fields["path"] != "/leagues" {
			t.Errorf("path field = %v, want /leagues", logger.calls[0].fields["path"])
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
}
