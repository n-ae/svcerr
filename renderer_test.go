package svcerr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRendererZeroConfigMatchesPackageDefault(t *testing.T) {
	// With a zero config and untouched globals, a Renderer's output must
	// be byte-identical to the package-level writers' - it's the same
	// machinery under an instance snapshot.
	r, err := NewRenderer(RendererConfig{})
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	cases := map[string]error{
		"not found":  NewNotFoundError("league", "12345"),
		"rate limit": NewRateLimitError("api", 100, 30),
		"auth":       NewAuthenticationError("token_expired", "session expired"),
		"internal":   NewInternalError("billing", "charge failed"),
	}
	for name, sample := range cases {
		t.Run(name, func(t *testing.T) {
			viaRenderer := httptest.NewRecorder()
			gotRes := r.JSON(viaRenderer, sample)

			viaPackage := httptest.NewRecorder()
			wantRes := WriteJSONResult(viaPackage, sample)

			if gotRes != wantRes {
				t.Errorf("WriteResult = %+v, want %+v", gotRes, wantRes)
			}
			if viaRenderer.Code != viaPackage.Code || viaRenderer.Body.String() != viaPackage.Body.String() {
				t.Errorf("renderer response %d %q diverged from package response %d %q",
					viaRenderer.Code, viaRenderer.Body.String(), viaPackage.Code, viaPackage.Body.String())
			}
			for _, h := range []string{"Content-Type", "Retry-After", "WWW-Authenticate", "X-Content-Type-Options"} {
				if viaRenderer.Header().Get(h) != viaPackage.Header().Get(h) {
					t.Errorf("%s = %q, want %q", h, viaRenderer.Header().Get(h), viaPackage.Header().Get(h))
				}
			}
		})
	}
}

func TestRendererIsolatedFromGlobals(t *testing.T) {
	const globalCode ErrorCode = "RENDERER_TEST_GLOBAL_CODE"
	t.Cleanup(func() {
		customStatusMu.Lock()
		delete(customStatusCode, globalCode)
		customStatusMu.Unlock()
		SetDefaultAuthenticateChallenge("")
		SetHeaderPolicy(HeaderPolicy{})
	})

	if err := RegisterStatusCode(globalCode, http.StatusTeapot); err != nil {
		t.Fatal(err)
	}
	SetDefaultAuthenticateChallenge(`Bearer realm="global"`)
	SetHeaderPolicy(HeaderPolicy{KeepContentEncoding: true})

	r, err := NewRenderer(RendererConfig{})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("global registry does not reach the renderer", func(t *testing.T) {
		w := httptest.NewRecorder()
		res := r.JSON(w, New(globalCode, "x"))
		if res.Status != http.StatusInternalServerError {
			t.Errorf("Status = %d, want the built-in 500 default - a Renderer must not consult the global registry", res.Status)
		}
		if got := WriteJSON(httptest.NewRecorder(), New(globalCode, "x")); got != http.StatusTeapot {
			t.Errorf("package-level status = %d, want the globally registered %d", got, http.StatusTeapot)
		}
	})

	t.Run("global challenge default does not reach the renderer", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.JSON(w, NewAuthenticationError("token_expired", "session expired"))
		if got := w.Header().Get("WWW-Authenticate"); got != "" {
			t.Errorf("WWW-Authenticate = %q, want empty - the global default must not reach the renderer", got)
		}
	})

	t.Run("global header policy does not reach the renderer", func(t *testing.T) {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Encoding", "gzip")
		r.JSON(w, NewInternalError("test", "boom"))
		if got := w.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding = %q, want cleared - the global KeepContentEncoding must not reach the renderer", got)
		}
	})
}

func TestRendererConfigDoesNotLeakToPackageWriters(t *testing.T) {
	const rendererCode ErrorCode = "RENDERER_TEST_INSTANCE_CODE"
	r, err := NewRenderer(RendererConfig{
		StatusCodes:                  map[ErrorCode]int{rendererCode: http.StatusUnprocessableEntity},
		DefaultAuthenticateChallenge: `Bearer realm="instance"`,
	})
	if err != nil {
		t.Fatal(err)
	}

	if res := r.JSON(httptest.NewRecorder(), New(rendererCode, "x")); res.Status != http.StatusUnprocessableEntity {
		t.Fatalf("renderer Status = %d, want 422", res.Status)
	}

	if got := WriteJSON(httptest.NewRecorder(), New(rendererCode, "x")); got != http.StatusInternalServerError {
		t.Errorf("package-level status = %d, want 500 - a renderer's StatusCodes must not leak into the global mapping", got)
	}
	w := httptest.NewRecorder()
	WriteJSON(w, NewAuthenticationError("token_expired", "session expired"))
	if got := w.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("package-level WWW-Authenticate = %q, want empty - a renderer's challenge must not leak", got)
	}
}

func TestTwoRenderersCoexist(t *testing.T) {
	const code ErrorCode = "RENDERER_TEST_SHARED_CODE"
	a, err := NewRenderer(RendererConfig{
		StatusCodes:                  map[ErrorCode]int{code: http.StatusConflict},
		DefaultAuthenticateChallenge: `Bearer realm="a"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewRenderer(RendererConfig{
		StatusCodes:                  map[ErrorCode]int{code: http.StatusUnprocessableEntity},
		DefaultAuthenticateChallenge: `Bearer realm="b"`,
	})
	if err != nil {
		t.Fatal(err)
	}

	if resA, resB := a.JSON(httptest.NewRecorder(), New(code, "x")), b.JSON(httptest.NewRecorder(), New(code, "x")); resA.Status != http.StatusConflict || resB.Status != http.StatusUnprocessableEntity {
		t.Errorf("statuses = %d/%d, want 409/422 - each renderer must serve its own mapping", resA.Status, resB.Status)
	}

	authErr := func() error { return NewAuthenticationError("token_expired", "session expired") }
	wA, wB := httptest.NewRecorder(), httptest.NewRecorder()
	a.JSON(wA, authErr())
	b.JSON(wB, authErr())
	if gotA, gotB := wA.Header().Get("WWW-Authenticate"), wB.Header().Get("WWW-Authenticate"); gotA != `Bearer realm="a"` || gotB != `Bearer realm="b"` {
		t.Errorf("challenges = %q/%q, want each renderer's own", gotA, gotB)
	}
}

func TestNewRendererValidatesStatusRange(t *testing.T) {
	for _, status := range []int{0, 200, 399, 600} {
		if _, err := NewRenderer(RendererConfig{StatusCodes: map[ErrorCode]int{"X": status}}); err == nil {
			t.Errorf("NewRenderer with status %d: error = nil, want the 400-599 validation error", status)
		}
	}
}

func TestNewRendererCopiesConfig(t *testing.T) {
	const code ErrorCode = "RENDERER_TEST_COPY_CODE"
	codes := map[ErrorCode]int{code: http.StatusConflict}
	r, err := NewRenderer(RendererConfig{StatusCodes: codes})
	if err != nil {
		t.Fatal(err)
	}

	codes[code] = http.StatusGone
	codes["RENDERER_TEST_ADDED_LATER"] = http.StatusLocked

	if res := r.JSON(httptest.NewRecorder(), New(code, "x")); res.Status != http.StatusConflict {
		t.Errorf("Status = %d, want the 409 captured at construction - mutating the caller's map must not reach the renderer", res.Status)
	}
	if res := r.JSON(httptest.NewRecorder(), New("RENDERER_TEST_ADDED_LATER", "x")); res.Status != http.StatusInternalServerError {
		t.Errorf("Status = %d, want 500 - entries added to the caller's map after construction must not appear", res.Status)
	}
}

func TestRendererLogging(t *testing.T) {
	t.Run("each render method logs one record with the standard fields", func(t *testing.T) {
		logger := &recordingLogger{}
		r, err := NewRenderer(RendererConfig{Logger: logger})
		if err != nil {
			t.Fatal(err)
		}

		r.JSON(httptest.NewRecorder(), NewNotFoundError("league", "1"))
		r.HTML(httptest.NewRecorder(), NewNotFoundError("league", "1"))
		r.Problem(httptest.NewRecorder(), NewNotFoundError("league", "1"))

		if len(logger.calls) != 3 {
			t.Fatalf("logger.calls = %d, want 3", len(logger.calls))
		}
		for i, call := range logger.calls {
			if call.fields["error_code"] != string(ErrCodeNotFound) || call.fields["http_status"] != http.StatusNotFound {
				t.Errorf("call %d fields = %v, want the standard error_code/http_status", i, call.fields)
			}
		}
	})

	t.Run("nil logger renders without logging", func(t *testing.T) {
		r, err := NewRenderer(RendererConfig{})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		if res := r.JSON(w, NewInternalError("test", "boom")); res.Status != http.StatusInternalServerError {
			t.Errorf("Status = %d, want 500", res.Status)
		}
	})
}

func TestRendererMiddleware(t *testing.T) {
	t.Run("panic replacement uses the renderer's own configuration", func(t *testing.T) {
		// Override the built-in INTERNAL_ERROR mapping on this instance so
		// the replacement's status proves the renderer's mapping (not the
		// built-ins or the global registry) served the recovery path.
		logger := &recordingLogger{}
		r, err := NewRenderer(RendererConfig{
			StatusCodes:          map[ErrorCode]int{ErrCodeInternal: http.StatusServiceUnavailable},
			RecoveryHeaderPolicy: HeaderPolicy{KeepContentEncoding: true},
			Logger:               logger,
		})
		if err != nil {
			t.Fatal(err)
		}

		handler := r.Middleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom")
		}))
		w := httptest.NewRecorder()
		w.Header().Set("Content-Encoding", "gzip")
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/leagues", nil))

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want the renderer's 503 override for INTERNAL_ERROR", w.Code)
		}
		if got := w.Header().Get("Content-Encoding"); got != "gzip" {
			t.Errorf("Content-Encoding = %q, want gzip preserved under the renderer's RecoveryHeaderPolicy", got)
		}
		var resp HTTPErrorResponse
		if jsonErr := json.Unmarshal(w.Body.Bytes(), &resp); jsonErr != nil {
			t.Fatalf("body is not valid JSON: %v", jsonErr)
		}
		if resp.Error.Code != ErrCodeInternal {
			t.Errorf("Error.Code = %v, want %v", resp.Error.Code, ErrCodeInternal)
		}
		if len(logger.calls) != 1 || logger.calls[0].fields["path"] != "/leagues" {
			t.Errorf("logger.calls = %+v, want one panic record with the request path", logger.calls)
		}
	})

	t.Run("commit tracking still protects committed responses", func(t *testing.T) {
		r, err := NewRenderer(RendererConfig{})
		if err != nil {
			t.Fatal(err)
		}
		handler := r.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("partial"))
			panic("after commit")
		}))

		rec := httptest.NewRecorder()
		expectAbortHandler(t, handler, rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Body.String() != "partial" {
			t.Errorf("body = %q, want only the handler's own bytes", rec.Body.String())
		}
	})
}
