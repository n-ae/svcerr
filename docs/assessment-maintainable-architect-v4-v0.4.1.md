# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v0.4.1
**Date:** 2026-07-16
**Reviewer:** maintainable-architect-v4
**Prior assessment:** [docs/assessment-maintainable-architect-v4-v0.4.0.md](assessment-maintainable-architect-v4-v0.4.0.md) (v0.4.0, 2026-07-16)

---

## Verdict

v0.4.1 fixes essentially every explicit priority from the v0.4.0 review. Error classification is consistent, informational responses are handled correctly, invalid status registrations are rejected, public details can be controlled, custom RFC 9457 type/instance values are supported, and stack traces are now captured efficiently as raw PCs and resolved lazily. The release reports 99.7% root-module test coverage.

I would now call svcerr production-ready for ordinary Go HTTP APIs.

I would still stop slightly short of calling its recovery middleware a completely transparent `http.ResponseWriter` wrapper. One important interface-preservation issue remains, plus two response-safety edge cases.

### What v0.4.1 fixed

| v0.4.0 finding | v0.4.1 result |
|---|---|
| Inner public message could cross an outer classification | Fixed |
| Unsupported `Flush()` poisoned commit tracking | Fixed |
| Informational 1xx treated as final | Fixed |
| Repeated final `WriteHeader` forwarded | Fixed |
| Invalid custom HTTP statuses accepted | Fixed |
| No explicit public-detail controls | Added |
| No custom RFC 9457 type/instance | Added |
| Stack frames formatted eagerly | Fixed; lazy resolution |
| Database transaction/migration codes unreachable through semantic constructor | Fixed |
| Stale adapter documentation | Fixed |

These are substantive changes, not just additional tests or documentation.

I also downloaded the tagged source and ran:

```
go test ./...
go test -race ./...
go vet ./...
```

They all passed. My available toolchain was Go 1.23.2, so I lowered only the local `go` directive from the release's Go 1.25 requirement before running them; that is not a claim of official Go 1.23 support.

**Verified against the actual v0.4.1 source in this repo** (four focused reproduction tests, all confirmed failing as described, then removed): the `RemovePublicDetail` message-leak gap, the stale-headers issue, the ignored-JSON-encoding-error issue, and the add/remove ordering issue all reproduce exactly as this review states.

---

## 1. The middleware still falsely advertises Flusher and Hijacker

This is the main remaining architectural issue.

`trackingResponseWriter` always has these methods:

```go
func (w *trackingResponseWriter) Flush()
func (w *trackingResponseWriter) Hijack() (...)
```

Therefore every handler wrapped by `RecoveryMiddleware` observes:

```go
_, supportsFlush := w.(http.Flusher)     // always true
_, supportsHijack := w.(http.Hijacker)   // always true
```

even when the underlying writer supports neither. v0.4.1 fixed the worst consequence of unsupported flushing — it no longer marks the response committed — but it did not preserve the original capability set.

That conflicts with the intended standard-library usage. Go documents `Flusher` and `Hijacker` as optional capabilities that handlers should detect through runtime assertions. In particular, HTTP/2 writers intentionally do not implement `Hijacker`.

### `ResponseController.Flush()` reports false success

On a writer that cannot flush:

```go
controller := http.NewResponseController(w)
err := controller.Flush()
```

should return an error matching `http.ErrNotSupported`. But because `trackingResponseWriter` itself implements `Flush()` and that method silently returns when the underlying writer lacks `Flusher`, the controller sees a successful flush.

Reproduced with a minimal non-flushing writer:

```
underlying implements http.Flusher: false
wrapped implements http.Flusher:    true
ResponseController.Flush():         nil
```

Go explicitly states that `ResponseController` should return `ErrNotSupported` when the writer cannot perform an operation.

`Hijack()` has a related problem. It returns:

```go
fmt.Errorf(
    "svcerr: underlying http.ResponseWriter does not implement http.Hijacker",
)
```

That error does not match `http.ErrNotSupported`, because the wrapper intercepts the controller before its `Unwrap()` traversal can discover the real underlying capability.

### Correct design

Use wrapper variants that implement only the capabilities present underneath:

```go
type trackingWriter struct {
    http.ResponseWriter
    wroteHeader bool
    status      int
}

type trackingFlusher struct {
    *trackingWriter
    flusher http.Flusher
}

func (w *trackingFlusher) Flush() {
    if !w.wroteHeader {
        w.wroteHeader = true
        w.status = http.StatusOK
    }
    w.flusher.Flush()
}

type trackingHijacker struct {
    *trackingWriter
    hijacker http.Hijacker
}
```

Then select the wrapper at runtime:

```go
func newTrackingWriter(w http.ResponseWriter) (
    http.ResponseWriter,
    *trackingWriter,
) {
    base := &trackingWriter{ResponseWriter: w}

    _, flushable := w.(http.Flusher)
    _, hijackable := w.(http.Hijacker)

    switch {
    case flushable && hijackable:
        return &trackingFlusherHijacker{trackingWriter: base}, base
    case flushable:
        return &trackingFlusher{
            trackingWriter: base,
            flusher:        w.(http.Flusher),
        }, base
    case hijackable:
        return &trackingHijacker{
            trackingWriter: base,
            hijacker:       w.(http.Hijacker),
        }, base
    default:
        return base, base
    }
}
```

For a fully transparent wrapper, also consider preserving `http.Pusher` and performance-related interfaces such as `io.ReaderFrom`.

(Carried over from the v0.4.0 review's item #2 - deliberately deferred as separate, larger work in both rounds so far.)

---

## 2. `RemovePublicDetail` does not fully hide a sensitive identifier

The new public-detail API is useful, but the README's sensitive-identifier example is incomplete:

```go
err := svcerr.NewNotFoundError("user", customerEmail)
err.RemovePublicDetail("resource_id")
```

`RemovePublicDetail` removes the identifier from the structured `details` map. However, `NewNotFoundError` also constructs its own message as:

```go
"user not found: " + resourceID
```

Because `NOT_FOUND` belongs to the categories whose own messages are public by default, the email remains in the response's `message`.

**Reproduced:**

```json
{
  "error": {
    "code": "NOT_FOUND",
    "message": "user not found: secret@example.com",
    "details": {
      "resource_type": "user"
    }
  }
}
```

So the resource ID is removed from one location but still exposed in another. This matters because the README explicitly presents an email address as the example of a sensitive identifier. RFC 9457 also warns that generated error details need to be scrutinized for privacy and information leakage.

The immediate documentation fix is:

```go
err := svcerr.NewNotFoundError("user", customerEmail)
err.RemovePublicDetail("resource_id")
err.SetPublicMessage("user was not found")
```

The safer package-level design would be to stop putting identifiers in the default public not-found message:

```go
func NewNotFoundError(resourceType, resourceID string) *NotFoundError {
    return &NotFoundError{
        BaseError: BaseError{
            code:    ErrCodeNotFound,
            message: resourceType + " was not found",
        },
        ResourceType: resourceType,
        ResourceID:   resourceID,
    }
}
```

The identifier can remain in internal context and be made public explicitly when appropriate.

At minimum, the README should state that suppressing `resource_id` does not redact the message automatically.

---

## 3. Recovery responses can inherit stale success-response headers

When a panic occurs before the response is committed, recovery writes its JSON error body using the handler's existing header map. `writeJSONErrorBody` replaces `Content-Type`, but does not remove `Content-Length`, `Content-Encoding`, `Trailer`, `ETag`, or other representation-specific headers the handler may have prepared before panicking.

```go
func handler(w http.ResponseWriter, _ *http.Request) {
    w.Header().Set("Content-Length", "999")
    w.Header().Set("Content-Encoding", "gzip")
    w.Header().Set("ETag", `"successful-response-etag"`)

    panic("boom")
}
```

**Reproduced:**

```
Status:           500
Content-Type:     application/json
Content-Length:   999
Content-Encoding: gzip
ETag:             "successful-response-etag"

Actual body length: 124 bytes
```

A stale `Content-Length` is particularly dangerous: the JSON body does not match the advertised length, and a real server writer can reject or truncate writes. A stale `Content-Encoding: gzip` tells clients that an uncompressed body is compressed.

The standard library's `http.Error` explicitly deletes `Content-Length` and sets `X-Content-Type-Options: nosniff` because the handler might have prepared headers for a successful representation before writing an error.

At minimum, every error writer should do:

```go
func prepareErrorHeaders(h http.Header, contentType string) {
    h.Del("Content-Length")
    h.Del("Trailer")

    h.Set("Content-Type", contentType)
    h.Set("X-Content-Type-Options", "nosniff")
}
```

For panic recovery, consider additionally removing representation metadata such as:

```go
h.Del("ETag")
h.Del("Last-Modified")
h.Del("Accept-Ranges")
```

`Content-Encoding` needs care because an outer compression middleware may legitimately own it. The correct behavior depends on middleware ordering, so this should either be documented or controlled through a configurable header-preparation function.

---

## 4. JSON encoding errors are ignored after the response is committed

The JSON and problem writers currently follow this sequence:

```go
w.WriteHeader(statusCode)
_ = json.NewEncoder(w).Encode(response)
```

The error returned from `Encode` is discarded.

This became more relevant in v0.4.1 because `SetPublicDetail` accepts an arbitrary `interface{}`:

```go
err.SetPublicDetail("anything", value)
```

A caller can therefore accidentally supply a value that JSON cannot encode, such as a channel, function, cyclic structure, or a custom marshaler returning an error.

**Reproduced:**

```go
err := svcerr.New(svcerr.ErrCodeInvalidInput, "invalid")
err.SetPublicDetail("bad", make(chan int))

status := svcerr.WriteJSON(w, err)
```

```
returned status: 400
HTTP status:     400
response body:   empty
reported error:  none
```

The API reports that it successfully wrote a 400 response, even though it emitted no usable error document.

### Better approach

Serialize before committing:

```go
body, marshalErr := json.Marshal(errResp)
if marshalErr != nil {
    body = []byte(`{"error":{"code":"INTERNAL_ERROR","message":"An unexpected error occurred."}}`)
    statusCode = http.StatusInternalServerError
}

prepareErrorHeaders(w.Header(), "application/json")
w.WriteHeader(statusCode)

_, writeErr := w.Write(append(body, '\n'))
```

A new API could report failures without breaking the existing convenience functions:

```go
func WriteJSONResult(
    w http.ResponseWriter,
    err error,
) (status int, writeErr error)
```

The existing `WriteJSON` could remain as a compatibility wrapper.

---

## 5. Custom RFC 9457 types still cannot define their title

v0.4.1 adds `SetProblemType(...)` and `SetProblemInstance(...)`, which is useful. But `WriteProblem` always sets:

```go
Title: http.StatusText(statusCode)
```

even when the type is no longer `about:blank`. The limitation is openly documented in the source and README.

For `about:blank`, the status phrase is correct. For a custom problem type, RFC 9457 defines `title` as a short summary of that problem type and says new problem-type definitions must document an appropriate title.

An additive solution:

```go
type ProblemTitler interface {
    ProblemTitle() (string, bool)
}

func (e *BaseError) SetProblemTitle(title string)
```

Then:

```go
title := http.StatusText(statusCode)
if pt, ok := node.(ProblemTitler); ok {
    if custom, set := pt.ProblemTitle(); set {
        title = custom
    }
}
```

This is an API-completeness improvement, not a production blocker.

---

## Smaller observations

### Public-detail operation ordering is surprising

**Reproduced.** `extractErrorDetails` applies additions first and removals afterward. Consequently:

```go
err.RemovePublicDetail("resource_id")
err.SetPublicDetail("resource_id", safeID)
```

still removes the key because `SetPublicDetail` does not clear the existing removal marker.

Making the latest operation win would be more intuitive:

```go
func (e *BaseError) SetPublicDetail(key string, value any) {
    delete(e.publicDetailRemovals, key)
    e.publicDetailAdditions[key] = value
}

func (e *BaseError) RemovePublicDetail(key string) {
    delete(e.publicDetailAdditions, key)
    e.publicDetailRemovals[key] = struct{}{}
}
```

### `safeLog` protects against nil, not logger panics

`safeLog` skips a nil logger, but a logger implementation that panics will still escape the error writer or replace the original recovered panic.

That may be an acceptable contract - loggers generally should not panic - but the name `safeLog` arguably suggests stronger isolation. Either rename it to `logIfPresent` or document that logger implementations must not panic.

### The Go version floor looks higher than necessary

The root module declares Go 1.25.0 while remaining dependency-free.

As noted above, the root code and test suite passed under my Go 1.23.2 environment after changing only the local `go` directive. That does not establish supported compatibility, but it suggests a lower minimum may be feasible. A CI matrix covering the intended oldest version and the current Go release would make the policy explicit. (Not independently re-verified this round - no second Go toolchain available in this environment to test against.)

---

## Revised rating

| Area | v0.4.0 | v0.4.1 |
|---|---|---|
| Problem selection | 9/10 | 9/10 |
| Scope and usability | 9/10 | 9.2/10 |
| Go idioms | 8/10 | 8.5/10 |
| Extensibility | 8/10 | 8.7/10 |
| Response safety | 8/10 | 8.5/10 |
| Production readiness | 8/10 | 8.5/10 |

---

## Priority for v0.4.2

1. Preserve `ResponseWriter` optional interfaces exactly instead of structurally advertising unsupported capabilities.
2. Correct the sensitive-identifier documentation and ideally remove resource IDs from default public not-found messages.
3. Clear stale `Content-Length` and other incompatible representation headers before writing errors.
4. Marshal JSON before committing the response and expose encoding/write failures.
5. Add a custom problem-title capability.
6. Make `SetPublicDetail`/`RemovePublicDetail` use last-operation-wins semantics.

After the first four changes, I would regard svcerr as robust production infrastructure, rather than simply a very good service-error package with a few edge-case caveats.
