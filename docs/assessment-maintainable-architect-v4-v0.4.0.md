# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v0.4.0 (root module); zerologadapter v0.4.1
**Date:** 2026-07-16
**Reviewer:** maintainable-architect-v4
**Prior assessment:** [docs/assessment-maintainable-architect-v4.md](assessment-maintainable-architect-v4.md) (v0.3.0, 2026-07-16)

---

## Verdict

v0.4.0 is a substantial improvement and fixes nearly every major issue identified in v0.3.0. The public-message policy is now category-based, classification details come from the outermost coded error, RFC 9457 titles are correct, pure renderers are available, nil loggers are safe, and the root module genuinely has no third-party dependencies.

I would now consider svcerr usable in production for conventional Go HTTP services. I would not yet call the recovery middleware fully robust infrastructure, because three reproducible edge cases remain:

1. An inner `PublicMessage` can cross an outer custom error classification.
2. The response-writer wrapper still falsely advertises optional HTTP capabilities.
3. Informational 1xx responses are incorrectly treated as final response commitment.

### What v0.4.0 fixed

| v0.3.0 finding | v0.4.0 result |
|---|---|
| Outer code combined with inner details or headers | Fixed |
| `Flush()` did not mark the response committed | Fixed for supported flushers |
| No `Unwrap()` for `ResponseController` | Added |
| Public-message policy depended on presence of a cause | Fixed; now category-based |
| RFC 9457 `about:blank` title was incorrect | Fixed with `http.StatusText` |
| Rendering required participation in logging | Added `WriteJSON`, `WriteHTML`, `WriteProblem` |
| Nil logger caused panic | Fixed |
| Root module pulled in zerolog | Adapter split into a separate module |
| Wrapped panic/internal errors could expose own text inconsistently | Fixed through category policy |

The package is also backed by a broad test suite and reports 98.5% root-module coverage.

---

## 1. An inner public-message override can still cross an outer classification

The structured-details problem was fixed correctly: `extractErrorDetails` now uses the same outermost coded node that determines the response code.

But public-message lookup still begins with:

```go
var pm PublicMessager
if errors.As(err, &pm) {
    if msg, ok := pm.PublicMessage(); ok {
        return msg
    }
}
```

That searches the entire chain independently. Only afterward does the function use `outermostCoded` for the category-based logic.

This is safe for the package's own wrappers because every `BaseError` implements `PublicMessager`; `errors.As` stops at the outer error. It breaks for a custom coded wrapper — which the package explicitly supports — that does not implement `PublicMessager`.

```go
type InternalWrapper struct {
    err error
}

func (e *InternalWrapper) Error() string          { return "internal failure" }
func (e *InternalWrapper) Unwrap() error          { return e.err }
func (e *InternalWrapper) Code() svcerr.ErrorCode { return svcerr.ErrCodeInternal }

inner := svcerr.NewNotFoundError("user", "secret@example.com")
inner.SetPublicMessage("account secret@example.com was not found")

outer := &InternalWrapper{err: inner}

fmt.Println(svcerr.GetErrorCode(outer))
// INTERNAL_ERROR

fmt.Println(svcerr.UserMessage(outer))
// account secret@example.com was not found
```

**Reproduced against the v0.4.0 source** (a `TestReproPublicMessageCross` test built from this exact scenario fails as described: `GetErrorCode` returns `INTERNAL_ERROR` while `UserMessage` returns the inner error's overridden message).

The response can therefore combine:

```json
{
  "code": "INTERNAL_ERROR",
  "message": "account secret@example.com was not found"
}
```

The fix is small: public-message lookup should operate on the already-selected coded node.

```go
func getUserFriendlyMessage(code ErrorCode, err error) string {
    node := outermostCoded(err)
    if node == nil {
        return defaultMessageForCode(code)
    }

    if pm, ok := node.(PublicMessager); ok {
        if msg, set := pm.PublicMessage(); set {
            return msg
        }
    }

    if mayExposeOwnMessage(code) {
        // Existing own-message logic.
    }

    return defaultMessageForCode(code)
}
```

A plain `fmt.Errorf("lookup: %w", inner)` still behaves correctly because the inner error remains the outermost coded node.

---

## 2. `trackingResponseWriter` still lies about optional capabilities

The wrapper always defines both:

```go
func (w *trackingResponseWriter) Flush()
func (w *trackingResponseWriter) Hijack() (...)
```

Consequently, every wrapped response writer appears to implement both `http.Flusher` and `http.Hijacker`, even when the underlying writer supports neither.

That conflicts with the standard-library contract. Go documents these as optional capabilities and tells handlers to test for them dynamically; notably, HTTP/2 deliberately does not support `Hijacker`.

### Unsupported Flush can suppress panic recovery

The current implementation does this:

```go
func (w *trackingResponseWriter) Flush() {
    if !w.wroteHeader {
        w.wroteHeader = true
        w.status = http.StatusOK
    }
    if f, ok := w.ResponseWriter.(http.Flusher); ok {
        f.Flush()
    }
}
```

When the underlying writer does not implement `Flusher`:

- A handler's `w.(http.Flusher)` assertion still succeeds.
- `Flush()` performs no actual flush.
- The wrapper nevertheless marks the response committed.
- If the handler then panics, recovery refuses to write the 500 response.
- The underlying response remains empty and uncommitted.

**Reproduced** with a minimal non-flushing `ResponseWriter` (`TestReproFlushOnUnsupportedWriter`): `Flush()` marks `wroteHeader = true` while the recorder's body stays empty and nothing was actually flushed.

It also breaks `http.ResponseController`. The controller is supposed to return an error matching `http.ErrNotSupported` when the underlying writer lacks a capability. Because the wrapper itself implements `Flush`, the controller invokes that method and receives a false success instead of unwrapping to discover that flushing is unsupported.

`Hijack` is less damaging because it returns an error when unsupported, but this still makes a normal runtime assertion report an unavailable capability as available.

### Correct approach

Construct wrapper variants that preserve the underlying interface set:

```go
type trackingWriter struct {
    http.ResponseWriter
    wroteHeader bool
    status      int
}

type trackingFlusher struct {
    *trackingWriter
    http.Flusher
}

type trackingHijacker struct {
    *trackingWriter
    http.Hijacker
}

type trackingFlusherHijacker struct {
    *trackingWriter
    http.Flusher
    http.Hijacker
}
```

A factory can select the correct variant. The actual `Flush` and `Hijack` methods need to record commitment before delegating, so embedded interfaces alone are not enough, but the key property is:

```go
_, ok := wrapped.(http.Flusher)
```

must equal:

```go
_, ok := underlying.(http.Flusher)
```

Adding `Unwrap()` was worthwhile, but it cannot correct capabilities the outer wrapper already claims to implement.

**Minimal interim fix** (addresses the concrete "suppress panic recovery" harm without the full wrapper-variant redesign): only mark the response committed if the underlying writer actually supports and performs the flush.

```go
func (w *trackingResponseWriter) Flush() {
    f, ok := w.ResponseWriter.(http.Flusher)
    if !ok {
        return
    }
    if !w.wroteHeader {
        w.wroteHeader = true
        w.status = http.StatusOK
    }
    f.Flush()
}
```

This does not fix the deeper "always advertises Flusher/Hijacker" misrepresentation (`http.ResponseController` would still get a false-success `nil` from `Flush()` when called through this wrapper on a non-flushing underlying writer) — only the specific commit-tracking corruption.

---

## 3. Informational responses are treated as final commitment

`WriteHeader` currently marks every status as committed:

```go
func (w *trackingResponseWriter) WriteHeader(status int) {
    if !w.wroteHeader {
        w.wroteHeader = true
        w.status = status
    }
    w.ResponseWriter.WriteHeader(status)
}
```

That is incorrect for informational responses such as 100 Continue, 102 Processing, and 103 Early Hints. Go permits any number of 1xx headers before the single final 2xx–5xx response (`net/http`'s own `ResponseWriter.WriteHeader` doc: "unlike other response headers, informational headers may be written multiple times").

```go
func handler(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusEarlyHints)
    panic("boom")
}
```

**Reproduced** (`TestRepro1xxCommitment`): calling `WriteHeader(103)` alone sets `wroteHeader = true` with `status = 103`. A handler that sends an Early Hints response and then panics leaves `RecoveryMiddleware` believing a final response was already committed — it only logs, and the client never receives a real final status.

The tracking logic should distinguish informational headers from the final response:

```go
func (w *trackingWriter) WriteHeader(status int) {
    if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
        w.ResponseWriter.WriteHeader(status)
        return
    }

    if w.wroteHeader {
        return
    }

    w.wroteHeader = true
    w.status = status
    w.ResponseWriter.WriteHeader(status)
}
```

101 Switching Protocols deserves special handling because it represents a protocol transition and should generally be treated as committed.

This also fixes another small issue: repeated final calls to `WriteHeader` should not continually be forwarded after the first final status.

---

## 4. Public details are still implicit rather than opt-in

The text-message safety model is now strong, but structured details still automatically expose values including:

- Database operation names.
- External service names and upstream statuses.
- Resource identifiers.
- Rate-limit values and retry periods.

Examples:

```json
{
  "code": "EXTERNAL_API_ERROR",
  "details": {
    "service": "internal-fraud-engine",
    "status_code": 503
  }
}
```

```json
{
  "code": "NOT_FOUND",
  "details": {
    "resource_type": "user",
    "resource_id": "secret@example.com"
  }
}
```

These are often harmless, but not universally. RFC 9457 specifically warns that problem details must be scrutinized for implementation, security, and privacy leaks.

A safer extensibility model would be:

```go
type PublicDetailer interface {
    PublicDetails() map[string]any
}
```

Or explicitly distinguish:

```go
err.WithPublicDetail("field", "email")
err.WithInternalDetail("value", suppliedEmail)
```

The built-in semantic constructors could still opt in to sensible defaults, but applications would have a way to suppress identifiers or internal service names.

I consider this a design-hardening recommendation, not a v0.4.0 release blocker.

---

## 5. `RegisterStatusCode` accepts invalid statuses

The registration function stores any integer without validation:

```go
func RegisterStatusCode(code ErrorCode, status int)
```

That permits:

```go
svcerr.RegisterStatusCode(CodeOutOfStock, 0)
svcerr.RegisterStatusCode(CodeOutOfStock, 200)
svcerr.RegisterStatusCode(CodeOutOfStock, 999)
```

`ResponseWriter.WriteHeader` requires a valid 1xx–5xx status; invalid values can therefore cause a panic while the package is trying to write an error response.

Because this package renders errors, I would restrict custom mappings to 400–599:

```go
func RegisterStatusCode(code ErrorCode, status int) error {
    if code == "" {
        return errors.New("svcerr: error code must not be empty")
    }
    if status < 400 || status > 599 {
        return fmt.Errorf("svcerr: error status must be 400-599: %d", status)
    }

    customStatusMu.Lock()
    customStatusCode[code] = status
    customStatusMu.Unlock()
    return nil
}
```

Alternatively, panic immediately during registration. A startup-time panic is much easier to diagnose than a later panic inside an error handler.

(Carried over from the v0.3.0 "smaller findings" list — still unaddressed.)

---

## zerologadapter warning

The root `svcerr@v0.4.0` module is dependency-free and unaffected, but the separately tagged `zerologadapter@v0.4.0` was released with a stale module requirement and can fail with an ambiguous-import error. The release notes direct users to `zerologadapter@v0.4.1` instead.

So the safe combination is:

```
github.com/n-ae/svcerr@v0.4.0
github.com/n-ae/svcerr/zerologadapter@v0.4.1
```

There is also a stale package comment in `errors.go` saying the adapter lives in the same module, even though v0.4.0 moved it to a separate nested module. Confirmed still present:

> "(The zerologadapter subpackage, which does depend on zerolog, is optional and lives in this same module; ...)"

---

## Smaller remaining improvements

- `Context()` makes only a shallow map copy; nested slices, maps, and pointers remain mutable.
- `SetPublicMessage`, exported detail fields, and `RecaptureStackTrace` make error objects unsafe for concurrent mutation.
- Stack traces are eagerly formatted as at most ten strings; storing PCs from `runtime.Callers` and resolving frames when logging would be cheaper.
- `NewDatabaseError` and `WrapDatabaseError` always produce `DB_QUERY`, even when the supplied operation represents a transaction.
- `ProblemDetails` supports `Type` and `Instance`, but the built-in writer always emits `about:blank` and offers no configuration hook.
- Logging remains context-free, although the new pure renderers make it easy for applications to use request-aware reporting separately.

None of these would stop me adopting the package.

---

## Revised rating

| Area | v0.3.0 | v0.4.0 |
|---|---|---|
| Problem selection | 9/10 | 9/10 |
| Scope and usability | 8.5/10 | 9/10 |
| Go idioms | 8/10 | 8/10 |
| Extensibility | 7/10 | 8/10 |
| Response safety | 7/10 | 8/10 |
| Production readiness | 7/10 | 8/10 |

---

## Priority for the next root release

1. Resolve `PublicMessager` only from the outermost coded node.
2. Preserve optional `ResponseWriter` interfaces exactly (or, at minimum, apply the scoped `Flush()` fix that stops it from marking an unsupported flush as committed).
3. Do not treat ordinary informational 1xx headers as final commitment.
4. Validate custom HTTP status registrations.
5. Introduce explicit public-detail control.

After the first three fixes, I would regard svcerr as a robust, production-grade answer to this specific Go service-error problem — not merely a promising package.
