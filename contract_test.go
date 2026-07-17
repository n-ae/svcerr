// Package svcerr_test is the external-package contract suite: tests that
// exercise svcerr purely through its exported API, exactly as a consumer
// imports it. The internal suite (package svcerr) can reach private
// helpers, so by itself it never proves the public surface alone supports
// the package's documented use cases - these tests do, and they break if
// an exported name, response shape, or capability contract changes even
// when the internal tests still pass. Compiled, output-checked versions
// of the README's primary usage examples live in example_test.go.
package svcerr_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/n-ae/svcerr"
)

// Compile-time contract: every semantic type satisfies ErrorWithCode (and
// so error, Coder, Unwrap, StackTracer) through embedding. A consumer can
// rely on these assignments compiling.
var (
	_ svcerr.ErrorWithCode = (*svcerr.BaseError)(nil)
	_ svcerr.ErrorWithCode = (*svcerr.ValidationError)(nil)
	_ svcerr.ErrorWithCode = (*svcerr.DatabaseError)(nil)
	_ svcerr.ErrorWithCode = (*svcerr.ExternalAPIError)(nil)
	_ svcerr.ErrorWithCode = (*svcerr.AuthenticationError)(nil)
	_ svcerr.ErrorWithCode = (*svcerr.NotFoundError)(nil)
	_ svcerr.ErrorWithCode = (*svcerr.ConflictError)(nil)
	_ svcerr.ErrorWithCode = (*svcerr.RateLimitError)(nil)
	_ svcerr.ErrorWithCode = (*svcerr.InternalError)(nil)
)

// contractLogger implements svcerr.Logger the way a consumer would - via
// the exported interface only.
type contractLogger struct {
	entries []contractLogEntry
}

type contractLogEntry struct {
	level  svcerr.Level
	err    error
	fields map[string]interface{}
	msg    string
}

var _ svcerr.Logger = (*contractLogger)(nil)

func (l *contractLogger) Log(level svcerr.Level, err error, fields map[string]interface{}, msg string) {
	l.entries = append(l.entries, contractLogEntry{level: level, err: err, fields: fields, msg: msg})
}

func TestContractConstructorsAndStdlibErrors(t *testing.T) {
	cause := errors.New("underlying cause")

	cases := []struct {
		name     string
		err      error
		wantCode svcerr.ErrorCode
		wrapped  bool // errors.Is(err, cause) must hold
		as       func(error) bool
	}{
		{"New", svcerr.New(svcerr.ErrCodeQuotaExceeded, "quota exhausted"), svcerr.ErrCodeQuotaExceeded, false,
			func(err error) bool { var e *svcerr.BaseError; return errors.As(err, &e) }},
		{"Wrap", svcerr.Wrap(cause, svcerr.ErrCodeResourceConflict, "conflicting update"), svcerr.ErrCodeResourceConflict, true,
			func(err error) bool { var e *svcerr.BaseError; return errors.As(err, &e) }},
		{"NewValidationError", svcerr.NewValidationError("bad email", "email", "x@"), svcerr.ErrCodeInvalidInput, false,
			func(err error) bool {
				var e *svcerr.ValidationError
				return errors.As(err, &e) && e.Field == "email"
			}},
		{"WrapValidationError", svcerr.WrapValidationError(cause, "bad email", "email"), svcerr.ErrCodeInvalidInput, true,
			func(err error) bool {
				var e *svcerr.ValidationError
				return errors.As(err, &e) && e.Field == "email"
			}},
		{"NewDatabaseError", svcerr.NewDatabaseError("query", "select failed"), svcerr.ErrCodeDatabaseQuery, false,
			func(err error) bool {
				var e *svcerr.DatabaseError
				return errors.As(err, &e) && e.Operation == "query"
			}},
		{"NewDatabaseError transaction", svcerr.NewDatabaseError("transaction", "commit failed"), svcerr.ErrCodeDatabaseTransaction, false,
			func(err error) bool { var e *svcerr.DatabaseError; return errors.As(err, &e) }},
		{"WrapDatabaseError", svcerr.WrapDatabaseError(cause, "insert", "INSERT INTO t ..."), svcerr.ErrCodeDatabaseQuery, true,
			func(err error) bool {
				var e *svcerr.DatabaseError
				return errors.As(err, &e) && e.Query == "INSERT INTO t ..."
			}},
		{"NewExternalAPIError", svcerr.NewExternalAPIError("yahoo", "upstream 503", 503, "https://api.example.com"), svcerr.ErrCodeExternalAPI, false,
			func(err error) bool {
				var e *svcerr.ExternalAPIError
				return errors.As(err, &e) && e.Service == "yahoo" && e.StatusCode == 503
			}},
		{"WrapExternalAPIError", svcerr.WrapExternalAPIError(cause, "yahoo", "https://api.example.com", 502), svcerr.ErrCodeExternalAPI, true,
			func(err error) bool { var e *svcerr.ExternalAPIError; return errors.As(err, &e) }},
		{"NewAuthenticationError token_expired", svcerr.NewAuthenticationError("token_expired", "session expired"), svcerr.ErrCodeTokenExpired, false,
			func(err error) bool {
				var e *svcerr.AuthenticationError
				return errors.As(err, &e) && e.Reason == "token_expired"
			}},
		{"NewAuthenticationError default reason", svcerr.NewAuthenticationError("no_credentials", "log in first"), svcerr.ErrCodeUnauthorized, false,
			func(err error) bool { var e *svcerr.AuthenticationError; return errors.As(err, &e) }},
		{"WrapAuthenticationError", svcerr.WrapAuthenticationError(cause, "permission_denied", "not an admin"), svcerr.ErrCodePermissionDenied, true,
			func(err error) bool { var e *svcerr.AuthenticationError; return errors.As(err, &e) }},
		{"NewNotFoundError", svcerr.NewNotFoundError("league", "12345"), svcerr.ErrCodeNotFound, false,
			func(err error) bool {
				var e *svcerr.NotFoundError
				return errors.As(err, &e) && e.ResourceType == "league" && e.ResourceID == "12345"
			}},
		{"WrapNotFoundError", svcerr.WrapNotFoundError(cause, "league", "12345"), svcerr.ErrCodeNotFound, true,
			func(err error) bool { var e *svcerr.NotFoundError; return errors.As(err, &e) }},
		{"NewConflictError", svcerr.NewConflictError("user", "email", "already registered"), svcerr.ErrCodeAlreadyExists, false,
			func(err error) bool {
				var e *svcerr.ConflictError
				return errors.As(err, &e) && e.ConflictKey == "email"
			}},
		{"WrapConflictError", svcerr.WrapConflictError(cause, "user", "email", "already registered"), svcerr.ErrCodeAlreadyExists, true,
			func(err error) bool { var e *svcerr.ConflictError; return errors.As(err, &e) }},
		{"NewRateLimitError", svcerr.NewRateLimitError("api", 100, 30), svcerr.ErrCodeRateLimitExceeded, false,
			func(err error) bool {
				var e *svcerr.RateLimitError
				return errors.As(err, &e) && e.Limit == 100 && e.RetryAfter == 30
			}},
		{"WrapRateLimitError", svcerr.WrapRateLimitError(cause, "api", 100, 30), svcerr.ErrCodeRateLimitExceeded, true,
			func(err error) bool { var e *svcerr.RateLimitError; return errors.As(err, &e) }},
		{"NewInternalError", svcerr.NewInternalError("billing", "charge failed"), svcerr.ErrCodeInternal, false,
			func(err error) bool {
				var e *svcerr.InternalError
				return errors.As(err, &e) && e.Component == "billing"
			}},
		{"WrapInternalError", svcerr.WrapInternalError(cause, "billing", "charge failed"), svcerr.ErrCodeInternal, true,
			func(err error) bool { var e *svcerr.InternalError; return errors.As(err, &e) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := svcerr.GetErrorCode(tc.err); code != tc.wantCode {
				t.Errorf("GetErrorCode = %v, want %v", code, tc.wantCode)
			}
			if !tc.as(tc.err) {
				t.Error("errors.As to the concrete type failed (or its fields didn't survive construction)")
			}
			if got := errors.Is(tc.err, cause); got != tc.wrapped {
				t.Errorf("errors.Is(err, cause) = %v, want %v", got, tc.wrapped)
			}
			if tc.wrapped && errors.Unwrap(tc.err) == nil {
				t.Error("errors.Unwrap = nil for a wrapping constructor, want the cause")
			}
			if len(svcerr.GetStackTrace(tc.err)) == 0 {
				t.Error("GetStackTrace is empty, want the construction-site trace")
			}
		})
	}
}

func TestContractStatusMapping(t *testing.T) {
	cases := map[svcerr.ErrorCode]int{
		svcerr.ErrCodeInvalidInput:       http.StatusBadRequest,
		svcerr.ErrCodeUnauthorized:       http.StatusUnauthorized,
		svcerr.ErrCodePermissionDenied:   http.StatusForbidden,
		svcerr.ErrCodeNotFound:           http.StatusNotFound,
		svcerr.ErrCodeAlreadyExists:      http.StatusConflict,
		svcerr.ErrCodeRateLimitExceeded:  http.StatusTooManyRequests,
		svcerr.ErrCodeExternalAPI:        http.StatusBadGateway,
		svcerr.ErrCodeDatabaseConnection: http.StatusServiceUnavailable,
		svcerr.ErrCodeDatabaseQuery:      http.StatusInternalServerError,
		svcerr.ErrCodeInternal:           http.StatusInternalServerError,
		svcerr.ErrCodeNotImplemented:     http.StatusNotImplemented,
		"SOME_UNKNOWN_CODE":              http.StatusInternalServerError,
	}
	for code, want := range cases {
		if got := svcerr.HTTPStatusCode(code); got != want {
			t.Errorf("HTTPStatusCode(%q) = %d, want %d", code, got, want)
		}
	}
}

func TestContractJSONResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	logger := &contractLogger{}

	svcerr.WriteHTTPError(rec, svcerr.NewNotFoundError("league", "12345"), logger)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", nosniff)
	}

	// The response decodes into the exported shape - a consumer can rely
	// on HTTPErrorResponse/ErrorDetail for client-side handling too.
	var resp svcerr.HTTPErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body is not valid JSON: %v (body: %s)", err, rec.Body.String())
	}
	if resp.Error.Code != svcerr.ErrCodeNotFound {
		t.Errorf("Error.Code = %v, want %v", resp.Error.Code, svcerr.ErrCodeNotFound)
	}
	if resp.Error.Message != "league not found: 12345" {
		t.Errorf("Error.Message = %q, want the not-found message", resp.Error.Message)
	}
	if resp.Error.Details["resource_type"] != "league" || resp.Error.Details["resource_id"] != "12345" {
		t.Errorf("Details = %v, want resource_type/resource_id", resp.Error.Details)
	}

	if len(logger.entries) != 1 {
		t.Fatalf("logger entries = %d, want 1", len(logger.entries))
	}
	e := logger.entries[0]
	if e.level != svcerr.LevelWarn {
		t.Errorf("logged level = %v, want LevelWarn (4xx)", e.level)
	}
	if e.fields["error_code"] != string(svcerr.ErrCodeNotFound) || e.fields["http_status"] != http.StatusNotFound {
		t.Errorf("logged fields = %v, want error_code/http_status", e.fields)
	}
}

func TestContractProblemResponse(t *testing.T) {
	err := svcerr.NewNotFoundError("league", "12345")
	err.SetProblemType("https://example.com/problems/resource-not-found")
	err.SetProblemInstance("https://example.com/requests/abc123")
	err.SetProblemTitle("League not found")
	err.SetPublicDetail("hint", "check the id")

	rec := httptest.NewRecorder()
	svcerr.WriteProblem(rec, err)

	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var out map[string]interface{}
	if jsonErr := json.Unmarshal(rec.Body.Bytes(), &out); jsonErr != nil {
		t.Fatalf("body is not valid JSON: %v", jsonErr)
	}
	want := map[string]interface{}{
		"type":     "https://example.com/problems/resource-not-found",
		"title":    "League not found",
		"status":   float64(404),
		"detail":   "league not found: 12345",
		"instance": "https://example.com/requests/abc123",
		"code":     string(svcerr.ErrCodeNotFound),
		"hint":     "check the id", // extension member, flattened to the top level
	}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("out[%q] = %v, want %v", k, out[k], v)
		}
	}
}

func TestContractHTMLResponse(t *testing.T) {
	err := svcerr.NewValidationError("name must not contain <script> tags", "name", nil)

	rec := httptest.NewRecorder()
	status := svcerr.WriteHTML(rec, err)

	if status != http.StatusBadRequest || rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d/%d, want 400", status, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
	body := rec.Body.String()
	if want := "&lt;script&gt;"; !strings.Contains(body, want) {
		t.Errorf("body %q does not contain %q - message must be HTML-escaped", body, want)
	}
	if strings.Contains(body, "<script>") {
		t.Errorf("body %q contains unescaped <script>", body)
	}
}

func TestContractPublicMessagePolicy(t *testing.T) {
	// Client-input-shaped categories show their own message by default.
	validation := svcerr.NewValidationError("email is malformed", "email", nil)
	if msg := svcerr.UserMessage(validation); msg != "email is malformed" {
		t.Errorf("UserMessage(validation) = %q, want the error's own message", msg)
	}

	// Operational categories fall back to the generic per-code message.
	db := svcerr.WrapDatabaseError(errors.New("dial tcp: connection refused"), "query", "SELECT 1")
	if msg := svcerr.UserMessage(db); strings.Contains(msg, "connection refused") || strings.Contains(msg, "SELECT") {
		t.Errorf("UserMessage(db) = %q leaked operational detail", msg)
	}

	// SetPublicMessage overrides either category, and the rendered body
	// agrees with UserMessage.
	db.SetPublicMessage("We're having trouble reaching the database.")
	if msg := svcerr.UserMessage(db); msg != "We're having trouble reaching the database." {
		t.Errorf("UserMessage after SetPublicMessage = %q", msg)
	}
	rec := httptest.NewRecorder()
	svcerr.WriteJSON(rec, db)
	var resp svcerr.HTTPErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error.Message != svcerr.UserMessage(db) {
		t.Errorf("rendered message %q != UserMessage %q", resp.Error.Message, svcerr.UserMessage(db))
	}
}

func TestContractPublicDetails(t *testing.T) {
	err := svcerr.NewNotFoundError("user", "someone@example.com")
	err.SetPublicDetail("hint", "search by name instead")
	err.RemovePublicDetail("resource_id") // suppress the sensitive identifier

	rec := httptest.NewRecorder()
	svcerr.WriteJSON(rec, err)
	var resp svcerr.HTTPErrorResponse
	if jsonErr := json.Unmarshal(rec.Body.Bytes(), &resp); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if resp.Error.Details["hint"] != "search by name instead" {
		t.Errorf("Details[hint] = %v, want the added detail", resp.Error.Details["hint"])
	}
	if _, ok := resp.Error.Details["resource_id"]; ok {
		t.Error("Details[resource_id] present, want it suppressed by RemovePublicDetail")
	}
	if resp.Error.Details["resource_type"] != "user" {
		t.Errorf("Details[resource_type] = %v, want the built-in extraction untouched", resp.Error.Details["resource_type"])
	}
}

func TestContractAuthenticateChallenge(t *testing.T) {
	// Bare 401: no challenge unless the error provides one (documented
	// opt-in - this package can't invent an application's scheme/realm).
	bare := svcerr.NewAuthenticationError("token_expired", "session expired")
	rec := httptest.NewRecorder()
	if status := svcerr.WriteJSON(rec, bare); status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
	if h := rec.Header().Get("WWW-Authenticate"); h != "" {
		t.Errorf("WWW-Authenticate = %q, want empty without SetAuthenticateChallenge", h)
	}

	withChallenge := svcerr.NewAuthenticationError("token_expired", "session expired")
	withChallenge.SetAuthenticateChallenge(`Bearer realm="api", error="invalid_token"`)
	rec = httptest.NewRecorder()
	svcerr.WriteJSON(rec, withChallenge)
	if h := rec.Header().Get("WWW-Authenticate"); h != `Bearer realm="api", error="invalid_token"` {
		t.Errorf("WWW-Authenticate = %q, want the configured challenge", h)
	}

	// The application-wide default covers 401s whose error carries no
	// challenge of its own; an error-specific challenge still wins.
	svcerr.SetDefaultAuthenticateChallenge(`Bearer realm="api"`)
	defer svcerr.SetDefaultAuthenticateChallenge("")

	rec = httptest.NewRecorder()
	svcerr.WriteJSON(rec, svcerr.NewAuthenticationError("token_expired", "session expired"))
	if h := rec.Header().Get("WWW-Authenticate"); h != `Bearer realm="api"` {
		t.Errorf("WWW-Authenticate = %q, want the application-wide default", h)
	}

	rec = httptest.NewRecorder()
	svcerr.WriteJSON(rec, withChallenge)
	if h := rec.Header().Get("WWW-Authenticate"); h != `Bearer realm="api", error="invalid_token"` {
		t.Errorf("WWW-Authenticate = %q, want the error-specific challenge to beat the default", h)
	}
}

func TestContractHeaderPolicy(t *testing.T) {
	defer func() {
		svcerr.SetHeaderPolicy(svcerr.HeaderPolicy{})
		svcerr.SetRecoveryHeaderPolicy(svcerr.HeaderPolicy{})
	}()

	// Default: Content-Encoding cleared, validators kept.
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Encoding", "gzip")
	rec.Header().Set("ETag", `"abc"`)
	svcerr.WriteJSON(rec, svcerr.NewInternalError("test", "boom"))
	if rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("ETag") != `"abc"` {
		t.Errorf("default policy: Content-Encoding=%q ETag=%q, want cleared/kept",
			rec.Header().Get("Content-Encoding"), rec.Header().Get("ETag"))
	}

	// Both knobs flipped, through exported API only.
	svcerr.SetHeaderPolicy(svcerr.HeaderPolicy{KeepContentEncoding: true, ClearValidators: true})
	rec = httptest.NewRecorder()
	rec.Header().Set("Content-Encoding", "gzip")
	rec.Header().Set("ETag", `"abc"`)
	svcerr.WriteJSON(rec, svcerr.NewInternalError("test", "boom"))
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("ETag") != "" {
		t.Errorf("flipped policy: Content-Encoding=%q ETag=%q, want kept/cleared",
			rec.Header().Get("Content-Encoding"), rec.Header().Get("ETag"))
	}
}

// unprintableError is a consumer-defined error implementing only
// svcerr.Coder - the minimal capability the package documents as enough
// to participate in status mapping.
type unprintableError struct{}

func (*unprintableError) Error() string          { return "printer out of ink: tray 2" }
func (*unprintableError) Code() svcerr.ErrorCode { return "CONTRACT_PRINTER_ERROR" }

// declinedError is a consumer-defined error implementing Coder plus
// PublicMessager, without embedding BaseError.
type declinedError struct{}

func (*declinedError) Error() string          { return "card declined by processor: code 05" }
func (*declinedError) Code() svcerr.ErrorCode { return "CONTRACT_PAYMENT_DECLINED" }
func (*declinedError) PublicMessage() (string, bool) {
	return "Your payment was declined.", true
}

func TestContractCustomCapabilityTypes(t *testing.T) {
	t.Run("Coder alone joins status mapping via RegisterStatusCode", func(t *testing.T) {
		if err := svcerr.RegisterStatusCode("CONTRACT_PRINTER_ERROR", http.StatusUnprocessableEntity); err != nil {
			t.Fatalf("RegisterStatusCode: %v", err)
		}
		rec := httptest.NewRecorder()
		status := svcerr.WriteJSON(rec, &unprintableError{})
		if status != http.StatusUnprocessableEntity {
			t.Errorf("status = %d, want 422 (the registered mapping)", status)
		}
		var resp svcerr.HTTPErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Error.Code != "CONTRACT_PRINTER_ERROR" {
			t.Errorf("Error.Code = %v, want the custom code", resp.Error.Code)
		}
		// An unfamiliar code isn't in the safe-message category list, so
		// the error's own text (which may be operational) must not leak.
		if strings.Contains(resp.Error.Message, "tray 2") {
			t.Errorf("Error.Message = %q leaked the custom error's own text", resp.Error.Message)
		}
	})

	t.Run("PublicMessager on a custom type overrides the message", func(t *testing.T) {
		rec := httptest.NewRecorder()
		status := svcerr.WriteJSON(rec, &declinedError{})
		if status != http.StatusInternalServerError {
			t.Errorf("status = %d, want the 500 default for an unregistered code", status)
		}
		var resp svcerr.HTTPErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Error.Message != "Your payment was declined." {
			t.Errorf("Error.Message = %q, want the PublicMessager override", resp.Error.Message)
		}
	})
}

// minimalResponseWriter implements only http.ResponseWriter - no Flush, no
// Hijack - so the middleware contract about truthful capability
// advertising can be checked from outside.
type minimalResponseWriter struct {
	header http.Header
	status int
	body   []byte
}

func (w *minimalResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *minimalResponseWriter) Write(p []byte) (int, error) {
	w.body = append(w.body, p...)
	return len(p), nil
}

func (w *minimalResponseWriter) WriteHeader(status int) { w.status = status }

func TestContractRecoveryMiddleware(t *testing.T) {
	t.Run("panic becomes a 500 JSON response and one log record", func(t *testing.T) {
		logger := &contractLogger{}
		handler := svcerr.RecoveryMiddleware(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom")
		}))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/leagues", nil))

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
		var resp svcerr.HTTPErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("body is not valid JSON: %v", err)
		}
		if resp.Error.Code != svcerr.ErrCodeInternal {
			t.Errorf("Error.Code = %v, want %v", resp.Error.Code, svcerr.ErrCodeInternal)
		}
		if len(logger.entries) != 1 || logger.entries[0].level != svcerr.LevelError {
			t.Fatalf("logger entries = %+v, want one LevelError record", logger.entries)
		}
		if logger.entries[0].fields["path"] != "/leagues" {
			t.Errorf(`fields["path"] = %v, want /leagues`, logger.entries[0].fields["path"])
		}
	})

	t.Run("normal responses pass through untouched", func(t *testing.T) {
		logger := &contractLogger{}
		handler := svcerr.RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))

		if rec.Code != http.StatusCreated || rec.Body.String() != `{"ok":true}` {
			t.Errorf("response = %d %q, want 201 with the handler's body", rec.Code, rec.Body.String())
		}
		if len(logger.entries) != 0 {
			t.Errorf("logger entries = %+v, want none for an uneventful request", logger.entries)
		}
	})

	t.Run("committed response is aborted, not overwritten", func(t *testing.T) {
		handler := svcerr.RecoveryMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("partial"))
			panic("after commit")
		}))

		rec := httptest.NewRecorder()
		var recovered interface{}
		func() {
			defer func() { recovered = recover() }()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		}()

		if recovered != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler", recovered)
		}
		if rec.Body.String() != "partial" {
			t.Errorf("body = %q, want only the handler's own bytes - no appended error document", rec.Body.String())
		}
	})

	t.Run("capability advertising is truthful", func(t *testing.T) {
		probe := func(w http.ResponseWriter) (flusher, hijacker bool) {
			_, flusher = w.(http.Flusher)
			_, hijacker = w.(http.Hijacker)
			return flusher, hijacker
		}

		var gotFlusher, gotHijacker bool
		handler := svcerr.RecoveryMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			gotFlusher, gotHijacker = probe(w)
		}))

		// httptest.ResponseRecorder implements Flush but not Hijack.
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		if !gotFlusher || gotHijacker {
			t.Errorf("recorder: flusher=%v hijacker=%v, want true/false", gotFlusher, gotHijacker)
		}

		// A writer with neither capability must advertise neither.
		handler.ServeHTTP(&minimalResponseWriter{}, httptest.NewRequest(http.MethodGet, "/", nil))
		if gotFlusher || gotHijacker {
			t.Errorf("minimal writer: flusher=%v hijacker=%v, want false/false", gotFlusher, gotHijacker)
		}
	})
}

// disconnectedWriter fails every Write, like a client that hung up.
type disconnectedWriter struct {
	header http.Header
	status int
}

func (w *disconnectedWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *disconnectedWriter) Write([]byte) (int, error) {
	return 0, errors.New("write tcp: broken pipe")
}

func (w *disconnectedWriter) WriteHeader(status int) { w.status = status }

func TestContractWriteResult(t *testing.T) {
	t.Run("success reports the selected status and no errors", func(t *testing.T) {
		res := svcerr.WriteJSONResult(httptest.NewRecorder(), svcerr.NewNotFoundError("league", "12345"))
		if res.Status != http.StatusNotFound || res.RenderErr != nil || res.WriteErr != nil {
			t.Errorf("WriteResult = %+v, want Status=404 with nil errors", res)
		}
	})

	t.Run("delivery failure surfaces as WriteErr", func(t *testing.T) {
		res := svcerr.WriteJSONResult(&disconnectedWriter{}, svcerr.NewNotFoundError("league", "12345"))
		if res.Status != http.StatusNotFound {
			t.Errorf("Status = %d, want 404 (the classification still happened)", res.Status)
		}
		if res.WriteErr == nil {
			t.Error("WriteErr = nil, want the transport failure surfaced")
		}
	})

	t.Run("unencodable detail surfaces as RenderErr with a 500 fallback", func(t *testing.T) {
		err := svcerr.NewNotFoundError("league", "12345")
		err.SetPublicDetail("bad", func() {}) // funcs can't be JSON-encoded

		rec := httptest.NewRecorder()
		res := svcerr.WriteJSONResult(rec, err)
		if res.RenderErr == nil {
			t.Fatal("RenderErr = nil, want the marshal failure surfaced")
		}
		if res.Status != http.StatusInternalServerError || rec.Code != http.StatusInternalServerError {
			t.Errorf("Status = %d/%d, want the 500 fallback", res.Status, rec.Code)
		}
		var resp svcerr.HTTPErrorResponse
		if jsonErr := json.Unmarshal(rec.Body.Bytes(), &resp); jsonErr != nil {
			t.Fatalf("fallback body is not valid JSON: %v", jsonErr)
		}
		if resp.Error.Code != svcerr.ErrCodeInternal {
			t.Errorf("fallback Error.Code = %v, want %v", resp.Error.Code, svcerr.ErrCodeInternal)
		}
	})
}
