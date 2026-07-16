package svcerr

import (
	"encoding/json"
	"errors"
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
}
