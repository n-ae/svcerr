# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v0.5.0
**Date:** 2026-07-16
**Reviewer:** maintainable-architect-v4
**Prior assessment:** [docs/assessment-maintainable-architect-v4-v0.4.1.md](assessment-maintainable-architect-v4-v0.4.1.md) (v0.4.1, 2026-07-16)

---

## Verdict

v0.5.0 is the strongest release so far and closes most of the v0.4.1 review cleanly. The new `ResponseWriter` variants correctly preserve direct `http.Flusher` and `http.Hijacker` assertions, and `SetProblemTitle` completes the RFC 9457 customization API. The release reports 99.7% root-module coverage and 100% adapter coverage.

I would consider it production-ready for ordinary Go JSON/HTML services, but not yet completely transparent or failure-proof HTTP infrastructure. I reproduced three remaining correctness issues:

1. `ResponseController.Flush()` can still lose an underlying `FlushError`.
2. Panic recovery can send plain JSON while retaining `Content-Encoding: gzip`.
3. Serialization fallback is invisible to logging and callers.

There is also an HTTP standards gap for 401 Unauthorized responses.

**Verified against the actual v0.5.0 source in this repo** (three focused reproduction tests, all confirmed failing as described, then removed): the `FlushError` shadowing, the stale `Content-Encoding` on panic recovery, and the logged/rendered code mismatch on marshal-fallback all reproduce exactly as this review states. Also independently confirmed: the README's "each has a New\*/Wrap\* constructor" claim is false for `AuthenticationError`/`NotFoundError`/`ConflictError`/`RateLimitError` (only `New*` exists for those four), and `WWW-Authenticate` is never set anywhere in the source.

### What v0.5.0 fixed

| Previous finding | v0.5.0 |
|---|---|
| Wrapper always advertised Flusher | Fixed |
| Wrapper always advertised Hijacker | Fixed |
| Unsupported controller operations reported false success | Mostly fixed; one `FlushError` edge remains |
| No custom RFC 9457 title | Fixed |
| JSON committed before marshaling | Fixed |
| Stale `Content-Length` and `Trailer` | Fixed |
| Public-detail operation ordering | Fixed |
| Sensitive not-found identifier documentation | Fixed, although behavior intentionally remains |
| Root logging dependency | Still clean; root module has no third-party dependency |

The four wrapper variants are a good, idiomatic correction to the previous design. A handler now sees `http.Flusher` and `http.Hijacker` only when the underlying writer implements those interfaces.

---

## 1. `ResponseController.Flush()` can lose `FlushError`

This is the most concrete remaining middleware defect.

Go's `ResponseController` recognizes both:

```go
Flush()
FlushError() error
```

The error-returning form exists specifically so a wrapper can report a failed flush. **Confirmed in the stdlib source** (`net/http/responsecontroller.go`): `ResponseController.Flush()` checks `interface{ FlushError() error }` on the current writer in its unwrap loop *before* checking plain `http.Flusher` - and it does not continue unwrapping past a match.

svcerr detects only:

```go
flusher, flushable := w.(http.Flusher)
```

Its returned `flushTracker` implements `Flush()`, but not `FlushError()`. Because `flushTracker` matches the `Flusher` case in `ResponseController`'s switch on the very first iteration (checking the wrapper itself, not yet the underlying writer), the traversal never reaches `Unwrap()` to discover the underlying writer's `FlushError` method.

Reproduced with:

```go
type writer struct {
    http.ResponseWriter
}

func (*writer) Flush() {}

func (*writer) FlushError() error {
    return errors.New("flush failed")
}
```

The result through the middleware wrapper:

```
underlying FlushError():                    "flush failed"
http.NewResponseController(wrapped).Flush(): nil
```

So the wrapper now truthfully reports whether ordinary flushing exists, but it does not preserve the error semantics of that operation.

### Recommended fix

Add conditional variants for the private interface:

```go
type flushErrorer interface {
    FlushError() error
}

type flushErrorTracker struct {
    *trackingResponseWriter
    flusher      http.Flusher
    flushErrorer flushErrorer
}

func (w *flushErrorTracker) Flush() {
    commitOnFlush(w.trackingResponseWriter, w.flusher)
}

func (w *flushErrorTracker) FlushError() error {
    err := w.flushErrorer.FlushError()
    if err == nil && !w.wroteHeader {
        w.wroteHeader = true
        w.status = http.StatusOK
    }
    return err
}
```

It should be instantiated only when the underlying writer actually implements `FlushError() error`.

---

## 2. Recovery can retain a stale `Content-Encoding`

`prepareErrorHeaders` now correctly removes `Content-Length` and `Trailer`, but it deliberately retains `Content-Encoding`, `ETag`, `Last-Modified`, and `Accept-Ranges` (matching `net/http`'s own `http.Error` scope, by design from the previous round).

That decision can corrupt panic responses specifically.

Reproduced with this handler:

```go
func handler(w http.ResponseWriter, _ *http.Request) {
    w.Header().Set("Content-Encoding", "gzip")
    panic("boom")
}
```

v0.5.0 generated:

```
Status:           500
Content-Encoding: gzip
Body:             {"error":{"code":"INTERNAL_ERROR", ...}}
```

The body was ordinary, uncompressed JSON. A client honoring the header will attempt to decompress it and can fail with an invalid-gzip error.

This is especially plausible with middleware ordering:

```
Recovery middleware
  └── Compression middleware
        └── Handler
```

If the compression middleware sets the header and then the handler panics, recovery writes directly through its own writer rather than through the abandoned compression writer. The compression header survives, but the replacement body is not compressed.

### Recommended fix

Use stricter cleanup specifically for panic replacement responses:

```go
func prepareRecoveryHeaders(h http.Header, contentType string) {
    h.Del("Content-Length")
    h.Del("Content-Encoding")
    h.Del("Trailer")
    h.Del("ETag")
    h.Del("Last-Modified")
    h.Del("Accept-Ranges")

    h.Set("Content-Type", contentType)
    h.Set("X-Content-Type-Options", "nosniff")
}
```

The ordinary `WriteJSON` function can retain the current narrower policy if middleware-managed encoding must be supported. Recovery is different because it is replacing a representation that never completed.

At minimum, middleware ordering and this hazard should be documented.

---

## 3. 401 responses omit `WWW-Authenticate`

`ErrCodeUnauthorized`, `ErrCodeTokenExpired`, and `ErrCodeTokenInvalid` map to 401 Unauthorized. However, none of the response writers supplies a `WWW-Authenticate` challenge. **Confirmed: grepping the entire source for `WWW-Authenticate` returns no matches.**

HTTP semantics (RFC 7235 §3.1) require a server generating a 401 response to include at least one `WWW-Authenticate` challenge.

The library cannot safely invent the application's authentication scheme or realm, so a hard-coded value is not ideal. But a service using the current writer must remember to do this manually:

```go
w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
svcerr.WriteJSON(w, err)
```

That weakens the benefit of central HTTP mapping.

### Better API

Add optional public response headers:

```go
type HTTPHeaderer interface {
    HTTPHeaders() http.Header
}
```

Or provide an authentication-specific field:

```go
err := svcerr.NewAuthenticationError(
    "token_invalid",
    "invalid authentication token",
)
err.SetAuthenticateChallenge(`Bearer realm="api"`)
```

The response writer could then enforce that every 401 has a challenge or fall back to a configured default.

---

## 4. Serialization and write failures remain invisible

Marshaling before committing is a strong improvement. An unencodable public detail now causes a safe fallback 500 rather than an empty response. However, the actual marshaling error is discarded, and every body write error is also ignored:

```go
body, marshalErr := json.Marshal(errResp)

if marshalErr != nil {
    statusCode = http.StatusInternalServerError
    body = fallbackErrorBody(ErrCodeInternal)
}

w.WriteHeader(statusCode)
_, _ = w.Write(body)
```

Reproduced with:

```go
err := svcerr.NewValidationError("bad input", "name", nil)
err.SetPublicDetail("bad", make(chan int))

svcerr.WriteHTTPError(w, err, logger)
```

The client received:

```json
{
  "error": {
    "code": "INTERNAL_ERROR",
    "message": "An internal error occurred. Please contact support if the problem persists."
  }
}
```

But the logger received approximately:

```
error_code: INVALID_INPUT
http_status: 500
error: bad input
```

There was no field explaining that:

- Response serialization failed.
- The rendered code changed to `INTERNAL_ERROR`.
- The public detail contained an unsupported value.

This makes an accidental channel, function, cyclic object, or broken `MarshalJSON` implementation difficult to diagnose.

A network write failure is even less visible: `WriteJSON` still returns only the intended status code.

### Recommended result type

```go
type WriteResult struct {
    Status       int
    Code         ErrorCode
    FallbackUsed bool
    Err          error
}

func WriteJSONResult(
    w http.ResponseWriter,
    err error,
) WriteResult
```

Compatibility functions can remain:

```go
func WriteJSON(w http.ResponseWriter, err error) int {
    return WriteJSONResult(w, err).Status
}
```

`WriteHTTPError` should include rendering failures in its log fields:

```
original_error_code=INVALID_INPUT
response_error_code=INTERNAL_ERROR
response_render_error="json: unsupported type: chan int"
```

---

## 5. The wrapper still drops other optional interfaces

The v0.5.0 release correctly promises preservation of `Flusher` and `Hijacker`; it does not preserve all interfaces that an underlying `ResponseWriter` might implement.

For example, an HTTP/2 writer might implement `http.Pusher`. After wrapping:

```go
_, supported := wrapped.(http.Pusher)
```

returns false because none of the four tracking variants defines `Push`. Go still exposes `Pusher` as the interface for HTTP/2 server push.

This is less important today because browser HTTP/2 push usage is limited, but the wrapper is not fully transparent. It may also discard performance-oriented interfaces such as `io.ReaderFrom`.

I would either:

- Explicitly document that only `Flusher` and `Hijacker` are preserved, or
- Add `Pusher` preservation, or
- Use/test a generalized interface-preserving response-writer approach.

---

## 6. The README overstates the constructor symmetry

The README says all semantic error types have a `New*`/`Wrap*` constructor pair.

The actual API has pairs for `ValidationError`, `DatabaseError`, `ExternalAPIError`, `InternalError`, but only `New*` constructors for `AuthenticationError`, `NotFoundError`, `ConflictError`, `RateLimitError`. **Confirmed by grepping `errors.go` for `^func New`/`^func Wrap`** - exactly as this review states.

This is more than a documentation typo. Consider translating a repository error while preserving its identity:

```go
record, err := repo.Find(id)
if errors.Is(err, sql.ErrNoRows) {
    return nil, svcerr.WrapNotFoundError(
        err,
        "user",
        id,
    )
}
```

That API does not exist. The options are:

- Use `NewNotFoundError` and lose the cause.
- Use generic `Wrap` and lose the `NotFoundError` type and automatic resource details.
- Build the type manually, which private `BaseError` fields prevent.

Either add the missing wrappers or change the README claim.

---

## Documented sharp edges

### Sensitive resource IDs remain public by default

`NewNotFoundError("user", email)` still puts the identifier in both the message and structured details. The README now documents correctly that removing `resource_id` alone is insufficient and demonstrates also setting a safe public message.

That is acceptable as an explicit design choice, although a safe-by-default constructor would avoid embedding the ID in its public message.

### Internal topology is still exposed in structured details

Database operation names, external service names, and upstream status codes are public by default, even though their textual messages are sanitized. Applications can suppress these with `RemovePublicDetail`, but I would still lean toward opt-in details for database and external-service categories.

### Custom codes need explicit public messages

An application code such as:

```go
const ErrCodeOutOfStock svcerr.ErrorCode = "OUT_OF_STOCK"
```

can register an HTTP status, but its own message is not in the built-in safe-category list. Unless `SetPublicMessage` is called, the response message becomes the generic "An unexpected error occurred." This is secure, but it should be called out directly in the custom-code documentation.

---

## Test assessment

The repository's ordinary test suite and `go vet` passed in my local environment after changing only the local `go` directive from 1.25 to the installed Go 1.23 toolchain. I also added focused tests that reproduced:

- Lost `FlushError`.
- Stale gzip encoding on a panic response.
- The serialization-fallback/log-classification mismatch.

The complete race suite did not finish within my execution timeout, so I cannot independently confirm the release's race behavior.

---

## Revised rating

| Area | v0.4.1 | v0.5.0 |
|---|---|---|
| Problem selection | 9/10 | 9/10 |
| Scope and usability | 9.2/10 | 9.3/10 |
| Go idioms | 8.5/10 | 8.8/10 |
| Extensibility | 8.7/10 | 8.8/10 |
| Response safety | 8.5/10 | 8.7/10 |
| Middleware transparency | 7.5/10 | 8.5/10 |
| Production readiness | 8.5/10 | 8.8/10 |

---

## Priority for v0.5.1

1. Preserve `FlushError() error` and mark the response committed only after a successful flush.
2. Clear incompatible representation headers, particularly `Content-Encoding`, when recovery replaces a panicked response.
3. Add a configurable mechanism for HTTP headers so 401 responses can include `WWW-Authenticate`.
4. Return or log marshaling and body-write failures, including the distinction between original and rendered classifications.
5. Add the missing typed `Wrap*` constructors or correct the README.
6. Decide whether to preserve `http.Pusher` and other optional writer interfaces.

After the first four, I would regard svcerr as robust production infrastructure, including for compressed APIs and custom HTTP writer stacks - not merely a strong package for conventional handlers.
