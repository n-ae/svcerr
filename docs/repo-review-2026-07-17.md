---
title: Repository review — 2026-07-17
date: 2026-07-17
reviewer: Codex
reviewed_commit: a4f32f5902afe535be39dd2d06f739032df6feae
reviewed_release: v0.6.3
status: Complete
---

# Repository review — 2026-07-17

## Verdict

The repository is small, cohesive, unusually well documented, and strongly
tested. The root module remains dependency-free, the zerolog adapter is cleanly
isolated in its own module, public-versus-internal error classification is
handled consistently, and v0.6.3 correctly closes the direct cases raised in
the preceding assessment: committed-response panics are aborted, failed
`FlushError` calls are treated as potentially committed, render/write failures
have a public result API, and logger panics are contained.

One important composition bug remains in `RecoveryMiddleware`: commit tracking
can be bypassed when `http.ResponseController` follows an `Unwrap` chain. I
reproduced this through a real `httptest.Server`; the client receives a clean
`200` carrying the `INTERNAL_ERROR` JSON after a flush and panic. This is the
same externally misleading result that v0.6.3's committed-response fix is
intended to prevent. I would fix this before describing the middleware as safe
to compose with arbitrary ResponseController-aware wrappers.

The review found one high-severity issue, one medium-severity issue, and three
low-severity consistency/documentation/test gaps.

## Findings

### High — H1: `ResponseController` can flush through `Unwrap` without updating the commit tracker

`newTrackingResponseWriter` selects its wrapper variant by checking optional
interfaces only on the immediate `ResponseWriter` (`http.go:962-983`). The base
tracker nevertheless exposes the immediate writer through `Unwrap`
(`http.go:799-805`). `http.ResponseController` follows that method repeatedly
until it finds `FlushError`, `Flusher`, or `Hijacker`.

That combination creates a bypass when an intermediate middleware wrapper
implements only `http.ResponseWriter` plus `Unwrap`:

```go
type unwrapOnly struct {
    http.ResponseWriter
}

func (w *unwrapOnly) Unwrap() http.ResponseWriter {
    return w.ResponseWriter
}
```

With a real server writer beneath `unwrapOnly`, this handler reproduces the
problem:

```go
func(w http.ResponseWriter, _ *http.Request) {
    _ = http.NewResponseController(w).Flush()
    panic("boom after flush")
}
```

The factory sees no flusher on the immediate `unwrapOnly` value and returns the
base tracker. `ResponseController.Flush` then unwraps past both the tracker and
the intermediate wrapper and flushes the real server writer directly. The
response is committed as 200, but `tw.wroteHeader` remains false. Recovery
therefore takes its uncommitted branch at `http.go:1055-1079`, attempts a 500,
and sees a successful body write, so it returns normally instead of aborting.

Observed through a real `httptest.Server`:

```text
status = 200
body = {"error":{"code":"INTERNAL_ERROR","message":"An internal error occurred. Please contact support if the problem persists."}}
server log = superfluous response.WriteHeader ... trackingResponseWriter.WriteHeader
```

This is not a contrived invalid wrapper: `ResponseController` explicitly
defines `Unwrap() http.ResponseWriter` as the mechanism by which middleware
wrappers preserve controller operations.

Recommended fix: make commit-aware capability discovery follow the same unwrap
chain as `ResponseController`, or return tracked proxies from the unwrap path so
controller operations cannot escape the shared state. Do not simply remove
`Unwrap`; that would regress deadlines and full-duplex support. Add regression
tests with an `unwrapOnly` layer for both `Flush` and `Hijack`, including at
least one real-server round trip for the flush/panic case.

### Medium — M1: invalid `WriteHeader` panics are falsely recorded as committed responses

`trackingResponseWriter.WriteHeader` sets `wroteHeader` and `status` before
delegating to the underlying writer (`http.go:783-788`). The standard server
and `httptest.ResponseRecorder` panic on a status outside their accepted
three-digit range before committing any bytes.

For a handler that calls `w.WriteHeader(99)`, the underlying writer panics, but
the tracker has already recorded a committed status of 99. Recovery enters the
committed branch (`http.go:1031-1052`) and re-panics with
`http.ErrAbortHandler` instead of writing the still-possible 500 response.

Observed through a real `httptest.Server`:

```text
GET error = EOF
```

Without the premature state update, there is no committed response preventing
normal panic recovery. This only affects invalid status calls made by wrapped
application handlers; svcerr's own mappings are protected by
`RegisterStatusCode`'s 400-599 validation.

Recommended fix: validate before mutating tracker state, or record the final
status only after the underlying `WriteHeader` returns successfully. Add tests
for values below 100 and above 999 through the full recovery middleware.

### Low — L1: HTML rate-limit responses discard the caller's `Retry-After`

`prepareErrorHeaders` deliberately deletes any pre-existing `Retry-After`
(`http.go:239-245`). The JSON and problem+json writers then re-add the current
`RateLimitError` value through `rateLimitRetryAfterHeader`
(`http.go:194-198`, `http.go:442-446`). `writeHTMLErrorBody` does not
(`http.go:315-332`).

Reproduction:

```go
w := httptest.NewRecorder()
svcerr.WriteHTML(w, svcerr.NewRateLimitError("api", 10, 60))
// w.Header().Get("Retry-After") == ""
```

The header is optional on a 429, so this is not a protocol violation, but it is
an inconsistent loss of data that the caller explicitly supplied and that the
other renderers preserve. Call `rateLimitRetryAfterHeader` in the HTML path
before `WriteHeader`, using the same outermost coded node as the other writers.

### Low — L2: the README still describes pre-v0.6.3 recovery and reporter usage

Two public-facing sections have drifted from the current code:

1. `README.md:126-129` says a panic after commitment “just logs.” v0.6.3 now
   logs and re-panics with `http.ErrAbortHandler`, intentionally closing the
   HTTP/1 connection or resetting the HTTP/2 stream. That transport behavior is
   material to callers and clients.
2. `README.md:424-433` directs users with an independent reporter to
   `WriteJSON`, passing only its status to the reporter. That function still
   discards render and delivery failures. The newly added
   `WriteJSONResult`/`WriteHTMLResult`/`WriteProblemResult` API exists for
   exactly this use case but is absent from the README.

Update both sections and make the reporter example inspect `RenderErr` and
`WriteErr` where appropriate.

### Low — L3: the `runtime.Goexit` test is not an end-to-end substitute for `panic(nil)`

`http_test.go:1609-1641` uses `runtime.Goexit` as a deterministic way to make
`recover()` return nil, then verifies a recorder contains a 500 response. This
does exercise the middleware's `rec == nil && !returnedNormally` branch, but it
does not reproduce network behavior: `Goexit` continues terminating the
server's connection goroutine after svcerr's defer returns, so net/http never
runs its normal `finishRequest` flush.

Against a real `httptest.Server`, the same handler produced client-side `EOF`,
not an HTTP 500. This does not disprove the old-mode `panic(nil)` fix; it means
the surrogate is equivalent only at the recovered-value level, not through
response finalization.

Keep the branch test, but add separate intent-specific coverage:

- run an actual `panic(nil)` test under the Go 1.20 floor or
  `GODEBUG=panicnil=1`;
- test `runtime.Goexit` through a real server and document whether EOF is the
  accepted outcome or whether the middleware should explicitly flush an error
  response before termination continues.

## Architecture and maintainability notes

- The root/adapter module split is sound. Core callers do not inherit zerolog
  or its transitive dependencies, while the adapter remains independently
  testable and versioned.
- `outermostCoded` provides one classification authority for status, public
  message, details, and response headers. This avoids the data-crossing bugs
  that existed in earlier releases.
- The response-writer capability matrix is now the main complexity hotspot.
  Direct-interface variants are well covered, but the effective state machine
  also includes arbitrary `Unwrap` chains. Table-driven tests should cover
  immediate and nested capability combinations before adding more wrapper
  variants.
- Existing documented trade-offs—limited optional-interface preservation,
  shallow `Context` copies, mutable errors, compression-wrapper ordering, and
  selective retention of representation headers—remain explicit and were not
  counted again as findings.

## Verification performed

Reviewed `main` at `a4f32f5902afe535be39dd2d06f739032df6feae`
(`v0.6.3` code plus the subsequent assessment-wording correction) with a clean
working tree before this report was added.

Using Go 1.26.5:

- `go test -count=1 ./...`: pass in both modules.
- `go test -race -count=1 ./...`: pass in both modules.
- `go test -shuffle=on -count=3 ./...`: pass in both modules.
- `go vet ./...`: pass in both modules with `GOWORK=off`.
- `go mod tidy -diff`: no diff in either module with `GOWORK=off`.
- `gofmt -l .`: no output.
- `golangci-lint` v2.12.2 over all Go source and test files: 0 issues.
- Coverage: 99.7% root module; 100.0% zerolog adapter.

Focused throwaway regression tests were used to reproduce H1, M1, L1, and the
real-server `Goexit` behavior, then removed. The Go 1.20 floor lane was not
independently executed during this review; the repository's CI matrix is
configured to run it.

## Recommended order

1. Fix H1 and add unwrap-chain real-server coverage.
2. Fix M1 and cover invalid status panics.
3. Align the HTML `Retry-After` path.
4. Refresh the README and split the nil-panic/`Goexit` tests by intent.
