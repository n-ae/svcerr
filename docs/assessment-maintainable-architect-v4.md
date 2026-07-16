# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v0.3.0 (HEAD: `63da7d6` "Add Flusher/Hijacker passthrough, configurable status codes, RFC 9457 support, and split ErrorWithCode")
**Date:** 2026-07-16
**Reviewer:** maintainable-architect-v4

---

## Verdict

v0.3.0 is a major improvement over v0.2.0. Most of the previous review's important findings were addressed correctly: package naming, wrapped-cause leakage, HTML injection, validation-value exposure, interface size, missing constructors, status mappings, panic double-logging, and partial-response awareness.

I would now describe svcerr as well-designed and close to production-ready, rather than unsafe. Before using it on a public-facing service, though, I would fix two remaining correctness issues:

1. Error classification can combine an outer error's code with an inner error's public details.
2. `Flush()` can commit a response without the recovery middleware noticing, allowing a panic response to corrupt the already-sent body.

The v0.3.0 release explicitly addresses nearly all the v0.2.0 findings and reports 98.5% test coverage.

### What v0.3.0 fixed well

| Previous issue | v0.3.0 |
|---|---|
| Package named `errors` | Renamed to `svcerr` |
| Wrapped cause leaked to HTTP clients | Fixed |
| Panic error text leaked | Fixed for wrapped panic errors |
| HTML message inserted unescaped | Uses `html.EscapeString` |
| Validation value exposed | Removed from response details |
| Large mandatory error interface | Split into `Coder`, `StackTracer`, `PublicMessager` |
| Missing constructors for many codes | Added generic `New` and `Wrap` |
| `NOT_IMPLEMENTED` mapped to 500 | Now 501 |
| All database errors mapped to 503 | Only connection failures remain 503 |
| Panic logged twice | Fixed |
| Panic response written after committed output | Partially fixed |
| No RFC problem-details writer | Added `WriteHTTPProblem` |
| No custom status mappings | Added `RegisterStatusCode` |

These changes are not cosmetic. They make the package considerably easier to extend and safer by default. The narrower capability interfaces are particularly Go-like: custom errors can participate in code extraction or stack reporting without implementing unrelated methods.

---

## 1. Outer error codes can leak inner error details

This is now the most important remaining issue.

The response code is selected through:

```go
func GetErrorCode(err error) ErrorCode {
    var c Coder
    if errors.As(err, &c) {
        return c.Code()
    }
    return ErrCodeInternal
}
```

That normally selects the outermost coded error. But `extractErrorDetails` independently scans the entire chain for specific concrete types such as `ValidationError`, `DatabaseError`, `NotFoundError`, and `RateLimitError`. The `Retry-After` header and logging metadata also perform their own independent scans.

For example:

```go
inner := svcerr.NewNotFoundError(
    "user",
    "secret@example.com",
)

err := svcerr.WrapInternalError(
    inner,
    "user_service",
    "unexpected repository result",
)

svcerr.WriteHTTPError(w, err, logger)
```

The result can conceptually be:

```json
{
  "error": {
    "code": "INTERNAL_ERROR",
    "message": "An internal error occurred. Please contact support if the problem persists.",
    "details": {
      "resource_type": "user",
      "resource_id": "secret@example.com"
    }
  }
}
```

So the package correctly hides the inner textual cause, but still publishes structured data from that inner cause.

I reproduced this against the v0.3.0 source with a focused test. The same inconsistency applies to headers: an outer `INTERNAL_ERROR` wrapping an inner `RateLimitError` can acquire the inner error's `Retry-After` header.

### Better model: classify once

The package should identify one authoritative application-error node, then derive everything from it:

```go
type Classification struct {
    Err           error
    Code          ErrorCode
    Status        int
    PublicMessage string
    PublicDetails map[string]any
    Headers       http.Header
    Stack         []string
}
```

Conceptually:

```go
func Classify(err error) Classification {
    node := findOutermostCodedError(err)

    return Classification{
        Err:           err,
        Code:          codeOf(node),
        Status:        HTTPStatusCode(codeOf(node)),
        PublicMessage: publicMessageOf(node),
        PublicDetails: publicDetailsOf(node),
        Headers:       publicHeadersOf(node),
        Stack:         GetStackTrace(err),
    }
}
```

Then JSON, HTML, RFC 9457, headers, and logging all use that classification.

A plain standard-library wrapper such as:

```go
fmt.Errorf("loading account: %w", notFoundErr)
```

should still discover the coded inner error. But once an outer svcerr error establishes a new classification, inner transport metadata should no longer escape through it.

I would also consider making details explicitly public:

```go
err.WithPublicDetail("field", "email")
```

rather than automatically assuming that identifiers, external service names, database operations, and upstream status codes are safe.

---

## 2. `Flush()` is not tracked as a committed response

The new `trackingResponseWriter` marks the response committed when `WriteHeader`, `Write`, or a successful `Hijack` occurs. That solves the ordinary "write body, then panic" case.

However, its `Flush` implementation only delegates:

```go
func (w *trackingResponseWriter) Flush() {
    if f, ok := w.ResponseWriter.(http.Flusher); ok {
        f.Flush()
    }
}
```

It does not set:

```go
w.wroteHeader = true
w.status = http.StatusOK
```

A flush normally commits the response headers, implicitly as 200 OK when no explicit status was written. Therefore this handler is problematic:

```go
func handler(w http.ResponseWriter, _ *http.Request) {
    w.(http.Flusher).Flush()
    panic("boom")
}
```

I reproduced the result: the underlying response is committed as 200, but recovery believes it is still uncommitted and appends the JSON `INTERNAL_ERROR` document.

That produces exactly the response corruption the new tracking mechanism intends to prevent.

The immediate fix is:

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

### The wrapper also misrepresents optional interfaces

Because `trackingResponseWriter` itself always defines `Flush` and `Hijack`, handlers always see:

```go
_, supportsFlush := w.(http.Flusher)   // always true
_, supportsHijack := w.(http.Hijacker) // always true
```

even when the underlying writer supports neither.

That matters because handlers commonly use interface assertions to detect whether streaming or hijacking is available. In HTTP/2, for example, hijacking is generally unavailable, but this wrapper advertises it and then returns an error.

The wrapper also lacks:

```go
func (w *trackingResponseWriter) Unwrap() http.ResponseWriter {
    return w.ResponseWriter
}
```

Go's `http.ResponseController` explicitly expects either the original response writer or a wrapper exposing `Unwrap`; otherwise operations such as deadlines, full-duplex handling, flushing, and hijacking may not reach the underlying writer.

The fully correct solution is to use wrapper variants that preserve exactly the optional interfaces supported by the underlying writer. That is slightly tedious without a dependency, but it avoids lying to handlers about capabilities.

---

## 3. Public-message safety is still based partly on whether there is a cause

The wrapped-error leak is fixed well:

```go
svcerr.Wrap(
    dbErr,
    svcerr.ErrCodeInternal,
    "query failed",
)
```

will not surface `dbErr.Error()` unless an explicit public message is set.

But a package error with no wrapped cause is considered safe to expose:

```go
if errors.As(err, &cu) && cu.Unwrap() == nil {
    return cu.Error()
}
```

That means these calls publish their supplied messages directly:

```go
svcerr.NewInternalError(
    "billing",
    "stripe secret sk_live_... rejected",
)

svcerr.NewDatabaseError(
    "query",
    "postgres at 10.0.0.12 rejected password xyz",
)
```

Both constructors create errors without a cause, so their `Error()` text becomes client-facing.

This is safer than v0.2.0, but the rule is fragile because it asks developers to understand that:

- `WrapInternalError` is sanitized.
- `NewInternalError` is not necessarily sanitized.
- Adding or removing a cause changes public behavior.

A stronger policy is based on the error code or category:

```go
func mayExposeOwnMessage(code ErrorCode) bool {
    switch code {
    case ErrCodeInvalidInput,
        ErrCodeMissingRequired,
        ErrCodeInvalidFormat,
        ErrCodeConstraintViolation,
        ErrCodeNotFound,
        ErrCodeAlreadyExists,
        ErrCodeResourceConflict,
        ErrCodeUnauthorized,
        ErrCodePermissionDenied,
        ErrCodeRateLimitExceeded:
        return true
    default:
        return false
    }
}
```

Internal, database, and external-service errors should always use generic messages unless `SetPublicMessage` is called explicitly.

Panic handling also becomes more consistent under such a policy. Currently `panic(error)` creates a wrapped internal error and gets the generic message, while `panic(string)` creates an unwrapped internal error whose own "panic recovered" message is published. No panic value leaks now, but the difference is unnecessary.

---

## 4. The RFC 9457 response is slightly non-conforming

`WriteHTTPProblem` emits:

```go
Type:  "about:blank",
Title: defaultMessageForCode(code),
```

For a 404, that produces approximately:

```json
{
  "type": "about:blank",
  "title": "The requested resource was not found.",
  "status": 404
}
```

RFC 9457 says that when `type` is `about:blank`, `title` should be the standard HTTP status phrase, such as "Not Found" for 404.

The straightforward correction is:

```go
Title: http.StatusText(statusCode),
```

Alternatively, support stable application problem-type URIs:

```json
{
  "type": "https://example.com/problems/resource-not-found",
  "title": "Resource not found",
  "status": 404,
  "code": "NOT_FOUND"
}
```

That would justify an application-specific title and give clients a stable semantic identifier beyond the HTTP status.

---

## 5. Logging is implementation-independent, but not fully decoupled

The root package source imports no logging implementation and uses a small `Logger` interface, which is good.

There are still two kinds of coupling.

First, response writers always log:

```go
func WriteHTTPError(w http.ResponseWriter, err error, logger Logger)
```

and `logError` unconditionally calls:

```go
logger.Log(...)
```

A nil logger therefore panics, and callers cannot use the standard renderer without also participating in the logging contract. I would expose pure rendering separately:

```go
func WriteJSON(w http.ResponseWriter, err error) int
func WriteHTML(w http.ResponseWriter, err error) int
func WriteProblem(w http.ResponseWriter, err error) int
```

Then optionally offer:

```go
type Reporter func(context.Context, Event)

func HandleError(
    ctx context.Context,
    w http.ResponseWriter,
    err error,
    report Reporter,
)
```

That would also allow request IDs and trace context to flow naturally.

Second, the module's `go.mod` directly requires zerolog because the adapter is shipped in the same module. Consequently, zerolog remains a module dependency even when callers only import the root package, although unused zerolog code will not normally be linked into the resulting executable.

For a literal "zero dependency on any logging library" claim, move the adapter to:

```
github.com/n-ae/svcerr-zerolog
```

or make it README example code.

---

## Smaller findings

- `RegisterStatusCode` accepts any integer. Registering 0, 200, or 999 can produce invalid error-response behavior or a later `WriteHeader` panic. It should validate 400–599 and return an error.
- `WriteHeader` forwards every invocation to the underlying writer even after recording the first one. It should normally return immediately after the first commit.
- Stack traces are still eagerly converted into ten formatted strings using `runtime.Caller`. Capturing program counters through `runtime.Callers` and resolving frames only when reported would be cheaper on error-heavy paths.
- `Context()` returns a new map but performs only a shallow copy. A map, slice, or pointer stored as a context value can still be mutated through the returned value.
- `SetPublicMessage` and `RecaptureStackTrace` mutate error objects. That is acceptable when errors are constructed and configured locally, but should be documented as unsafe once an error might be shared across goroutines.

---

## Revised rating

| Area | v0.2.0 | v0.3.0 |
|---|---|---|
| Problem selection | 9/10 | 9/10 |
| Scope and usability | 8/10 | 8.5/10 |
| Go idioms | 7/10 | 8/10 |
| Extensibility | 5/10 | 7/10 |
| Response safety | 3/10 | 7/10 |
| Production readiness | 4/10 | 7/10 |

---

## Priority for v0.3.1

1. Introduce one authoritative classification step so inner details and headers cannot contradict the outer code.
2. Mark `Flush()` as committed and preserve optional `ResponseWriter` interfaces correctly.
3. Make public-message behavior category-based rather than dependent on `Unwrap() == nil`.
4. Correct the `about:blank` title and optionally support custom problem-type URIs.
5. Separate pure rendering from reporting and move the zerolog adapter to a separate module.

After the first two fixes, I would be comfortable adopting svcerr in ordinary Go HTTP services. After the first four, it would be a strong candidate for the best focused package in this particular niche.
