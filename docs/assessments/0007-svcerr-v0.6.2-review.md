---
assessment: 0007
title: svcerr v0.6.2 review
date: 2026-07-17
status: Accepted
reviewer: maintainable-architect-v4
prior: 0006-svcerr-v0.6.1-review.md (v0.6.1)
---

# Assessment 0007: svcerr v0.6.2 review

## Executive summary

v0.6.2 (599b4f8, f8360ba) closes assessment 0006's J1-J5: `RecoveryMiddleware` now tracks whether the handler returned normally and treats an abnormal `nil` recover as a forced 500 (J1), body-write errors are surfaced through the logging writers and the recovery path (J3), stale `Retry-After`/`WWW-Authenticate` are cleared with the rest of `prepareErrorHeaders` (J4), and `NewRateLimitError` clamps a negative `retryAfter` (J5). `go vet`, `go test -count=1`, and `go test -race -count=1` pass clean on both modules; coverage is confirmed at 99.7% root via `go tool cover -func`.

This assessment was prompted by a second external review of v0.6.2 raising 7 new findings, distinct from the first review 0006 already handled. I reproduced all 7 directly against HEAD (tag `v0.6.2`) using throwaway test files, not by trusting the review's code snippets — two of its own snippets (`commitOnFlushError`, `writeJSONErrorBody`'s panic-path caller) paraphrase the real source rather than quoting it verbatim, though the paraphrase is behaviorally accurate. The two headline claims (a committed-response panic being reported to the client as a clean 200, and `commitOnFlushError` under-recording commitment) both reproduce exactly as described, including against real `net/http` stdlib source I read directly (`server.go:1720-1733`) rather than taking the review's characterization of Go's own behavior on faith. Both proposed fixes were independently verified to work and to not interact badly with svcerr's existing `defer`/`recover` structure (no double-catch, no double-logging, no infinite recursion).

### What actually reproduces

| # | External claim | Verdict |
|---|---|---|
| 1 | A panic after the response is committed is logged then swallowed — client sees a clean 200 with a truncated body | **Real**, reproduced via `httptest.Server`. See K1. |
| 2 | `commitOnFlushError` only marks committed on a nil error, but real `net/http` commits `WriteHeader(200)` before attempting the flush | **Real**, confirmed against Go stdlib source directly. See K2. |
| 3 | Pure rendering functions (`WriteJSON`/`WriteHTML`/`WriteProblem`) still discard write/render errors | **Real** — v0.6.2's J3 fix reached only the logging variants. See K3. |
| 4 | Compression header-clearing should be configurable via a `HeaderPolicy` | **Not a defect.** Already documented as an accepted trade-off (README "Not compatible with... compression wrappers", added in this release). Configurability is a reasonable enhancement, not a fix for a bug. |
| 5 | 401 responses need a renderer-level default `WWW-Authenticate` challenge | **Not a defect**, same accepted trade-off as 0006's rejection of this claim from the first review. Raised by two independent reviews now — see Recommendations. |
| 6 | ETag/Last-Modified/Accept-Ranges retention can describe a stale representation | **Real design trade-off, already deliberate** (http.go:201-204) but undocumented outside a source comment. See K4. |
| 7 | `safeLog` doesn't protect against the logger itself panicking mid-recovery | **Real**, reproduced — a panicking `Logger.Log` escapes svcerr's own `recover()` entirely and is only caught by `net/http`'s outer per-connection recovery, which drops the original panic's diagnostic log. See K5. |

## Findings

### Major

**K1 — A panic after the response is committed is logged but the client sees a clean, complete 200 (S-M).**

`RecoveryMiddleware`'s committed-response branch (http.go:962-974) logs and then falls through to `return` inside the deferred function — no re-panic. I reproduced this against a real `httptest.Server`, not `httptest.Recorder` (which can't show what the client actually sees):

```go
func handler(w http.ResponseWriter, _ *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    _, _ = io.WriteString(w, `{"ok":`)
    panic("boom")
}
```

Result: `status=200 content-length=6 body="{\"ok\":" bodyReadErr=<nil>` — a syntactically invalid JSON document delivered as a complete, successful response with no transport-level signal that anything went wrong. This affects any handler that writes part of its body before panicking: streaming JSON, SSE, progressive HTML rendering, large downloads.

I verified the proposed fix (`panic(http.ErrAbortHandler)` after logging, in place of `return`) against svcerr's actual `defer`/`recover` structure rather than assuming it's safe: the re-panic happens *inside* the already-executing deferred function, after `recover()` already consumed the original panic, so it propagates out of `RecoveryMiddleware` uncaught by its own defer — no double-catch, no infinite recursion. A reproduction of this exact shape produced exactly one log call and a client-visible transport error (`EOF`) instead of a clean 200. This matches `net/http`'s own documented behavior: `http.ErrAbortHandler` closes the HTTP/1 connection (or resets the HTTP/2 stream) without an additional "http: panic serving" log line, which is why the external review's choice of that specific sentinel (rather than a bare `panic(rec)`) is correct — a bare re-panic would print a second, redundant server-side stack trace on top of svcerr's own structured log.

One implementation note the external review didn't mention: `http_test.go:1550`'s existing regression test for this path (`"response already committed before panic is not appended to"`) calls `handler.ServeHTTP(w, req)` directly against an `httptest.Recorder`, with no enclosing server and no `recover()` of its own. Landing this fix as-is will make that specific subtest panic instead of returning — it needs to move to `httptest.NewServer` (as my reproduction did) or wrap the `ServeHTTP` call in its own `recover()` to observe and assert on the abort instead of a normal return. Not a flaw in the fix, just a concrete first step for whoever picks it up.

The same reasoning extends to the case where writing the *replacement* 500 itself fails (`writeErr != nil` at http.go:987-989, currently just an extra log field) — a partial replacement body has the identical "looks like success, isn't" problem and should get the same `panic(http.ErrAbortHandler)` treatment.

**K2 — `commitOnFlushError` under-records commitment relative to what `net/http` actually does (S-M).**

`commitOnFlushError` (http.go:774-781) only sets `tw.wroteHeader = true` when `fe.FlushError()` returns nil:

```go
func commitOnFlushError(tw *trackingResponseWriter, fe flushErrorer) error {
    err := fe.FlushError()
    if err == nil && !tw.wroteHeader {
        tw.wroteHeader = true
        tw.status = http.StatusOK
    }
    return err
}
```

I checked this against the real implementation rather than the external review's paraphrase of it — Go 1.26's `net/http/server.go:1724-1733`:

```go
func (w *response) FlushError() error {
    if !w.wroteHeader {
        w.WriteHeader(StatusOK)
    }
    err := w.w.Flush()
    e2 := w.cw.flush()
    if err == nil {
        err = e2
    }
    return err
}
```

`WriteHeader(200)` is committed unconditionally before the flush is even attempted — a flush failure (a `bufio.Writer` error from a broken connection partway through) can happen *after* the status line and headers are already on the wire. I reproduced the consequence with a fake `flushErrorer` that mimics this ordering: after a failed `FlushError()`, `tw.wroteHeader` was still `false`, and a subsequent simulated panic caused `writeJSONErrorBody` to issue a **second** `WriteHeader`/`Write` pair (`underlying.writeHeaderCalls = [200 500]`) onto a writer that, per real `net/http` semantics, had already committed the first one. The recorder in my reproduction silently kept the first status and appended the second body's bytes — on a real connection this is a corrupted response, not a clean second one.

The proposed fix (mark `wroteHeader = true` unconditionally before delegating to `FlushError()`) is correct for the dominant case, since it matches what the standard library's own `ResponseWriter` — the backing implementation nearly every real `flushErrorer` in production ultimately wraps — actually does. The external review already honestly caveats the one place this could be wrong (a nonstandard custom writer whose `FlushError()` can fail for a reason unrelated to transport commitment, e.g. an app-level validation check before anything is written) — I don't have a further correction to add there; the caveat as stated is accurate and the trade-off (an occasional suppressed-but-recoverable 500 vs. writing a second response onto a possibly-already-sent one) clearly favors the fix.

### Minor

**K3 — The pure rendering functions still discard write/render errors, unlike their logging counterparts (S).**

`WriteJSON` (http.go:120-123), `WriteHTML` (http.go:268-271), and `WriteProblem` (http.go:340-343) each call their `*ErrorBody` helper and keep only the status code:

```go
func WriteJSON(w http.ResponseWriter, err error) int {
    statusCode, _, _ := writeJSONErrorBody(w, err)
    return statusCode
}
```

This is a real residual gap from 0006's J3, not a new defect: J3's fix (f8360ba) threaded `renderErr`/`writeErr` into `logError`'s fields for `WriteHTTPError`/`WriteHTTPErrorHTML`/`WriteHTTPProblem` and `RecoveryMiddleware`, but the three `Write*` convenience functions — which exist specifically for callers who want to own their own reporting instead of using this package's `Logger` contract (see the doc comment on `WriteJSON`) — never got a way to see either error. A caller doing `status := svcerr.WriteJSON(w, err); myReporter.Report(ctx, err, status)` has no signal that the body never reached the client. The external review's proposed additive `WriteJSONResult`/`WriteResult`-shaped API (keeping the existing `int`-returning functions as thin wrappers) is the right shape — it doesn't break the existing signature, which matters since `WriteJSON`/`WriteHTML`/`WriteProblem` are the one part of this API a caller might reasonably use in a hot path or a `defer`.

**K4 — ETag/Last-Modified/Accept-Ranges retention is a deliberate, real trade-off documented only in a source comment (S, doc-only).**

`prepareErrorHeaders` (http.go:205-213) clears `Content-Length`/`Content-Encoding`/`Trailer`/`Retry-After`/`WWW-Authenticate` but, per the comment at http.go:201-204, deliberately leaves `ETag`, `Last-Modified`, and `Accept-Ranges` alone. This is a real, considered design choice (a normal error writer may legitimately want some inherited headers), and unlike Content-Encoding — which got an explicit README callout in this same release — it's not mentioned anywhere outside that source comment (`grep -n "ETag\|Last-Modified\|Accept-Ranges" README.md` returns nothing). The scenario the external review describes (a 500 replacing an abandoned successful representation, with a stale `ETag` describing content that no longer matches the error body) is plausible for `RecoveryMiddleware` specifically, even if it's a reasonable default for a plain `WriteJSON` call replacing a handler's own partial success-path headers. This is the same pattern 0006 flagged for J2/J4 before this release's fixes: a comment is not the same as documentation a caller finds before shipping.

**K5 — `safeLog` has no containment against the logger itself panicking (S, hardening).**

`safeLog` (http.go:681-686) tolerates a nil `Logger` but calls `logger.Log(...)` directly otherwise, with no `recover()`. I reproduced the consequence with a `Logger` whose `Log` method panics, run through `RecoveryMiddleware` behind a real `httptest.Server`: the logger's panic propagates out of svcerr's already-executing deferred function, uncaught by svcerr's own `recover()` (which already fired once, for the original handler panic), and is only caught by `net/http`'s own outer per-connection recovery — which prints `http: panic serving ...` to the server's error log and aborts the connection, but never emits svcerr's own structured "Panic recovered in HTTP handler" record. The original panic's diagnostic context (error code, stack trace, request path) is lost, replaced by a generic stdlib panic trace pointing at the logger, not the original bug. Confirmed this doesn't crash the test process — `net/http`'s per-connection recover is what contains it, not svcerr's — which is exactly the review's point: the *connection* survives, the *intended* error-handling path doesn't. A `recover()`-wrapped containment helper is cheap; the alternative (document that `Logger.Log` must never panic) pushes a correctness requirement onto every consumer's logging adapter with no enforcement.

## Accepted trade-offs (not findings)

- **Compression header-clearing is a fixed, unconfigurable policy (external claim 4).** Already documented as of this release (README "Not compatible with transparent, eagerly-header-setting compression wrappers"). A `HeaderPolicy`-style configurability knob is a legitimate enhancement for callers who need `RecoveryMiddleware`'s panic-replacement behavior to differ from a plain `WriteJSON` call's, but the current fixed policy is not itself a defect — it was assessed as such in 0006 (J2) and remains the same trade-off, just better documented now.
- **401 responses have no default `WWW-Authenticate` challenge (external claim 5).** Same trade-off 0006 already rejected as a finding for the first external review. Worth noting this is the second independent review to ask for a renderer-level default — see Recommendations, "this quarter."
- All trade-offs restated in 0005/0006 (Flusher/Hijacker-only preserved interfaces, opt-out structured details, shallow `Context()`, non-concurrent-safe error mutation, generic custom-code messages) still hold.

## Recommendations

**This week:**
1. Fix K1: re-panic with `http.ErrAbortHandler` after logging a committed-response panic (and after a failed replacement-body write). Update `http_test.go:1550`'s existing regression subtest to observe this through a real server or its own `recover()`, since it currently asserts a plain return.
2. Fix K2: mark `tw.wroteHeader = true` unconditionally in `commitOnFlushError` before delegating to `FlushError()`.

**This month:**
3. Add `WriteJSONResult`/`WriteHTMLResult`/`WriteProblemResult` alongside the existing `int`-returning functions, per K3, keeping the current signatures as thin wrappers so no caller breaks.
4. Add a containment wrapper around `logger.Log` in `safeLog` (K5) — cheap, and the failure mode (losing the original panic's diagnostics) is exactly the scenario `RecoveryMiddleware` exists to prevent.
5. Add a README note for K4 (ETag/Last-Modified/Accept-Ranges retention on error replacement), matching the treatment Content-Encoding got this release.

**This quarter (optional):**
6. Evaluate a renderer-level `DefaultAuthenticateChallenge` — now requested by two independent external reviews rather than hypothetically. Still shouldn't be built speculatively, but the bar for "real usage shows callers routinely forgetting it" (0006's stated condition) looks close to met.
7. Evaluate a `HeaderPolicy`-style split between normal-error and panic-replacement header handling (external claim 4/6's underlying ask) if K1/K2 land first and streaming/compression callers remain the primary friction point — don't build configurability for its own sake.

## Verification performed

Against this working tree (tag `v0.6.2`, commit `f8360ba`, no diff): `go vet ./...`, `go test -count=1 ./...`, `go test -race -count=1 ./...` clean on both modules. Coverage confirmed at 99.7% root via `go tool cover -func`. All 7 external claims reproduced or falsified with throwaway `_test.go` files added to and removed from the root package (not committed) — including a real `httptest.Server` round trip for K1 (not `httptest.Recorder`, which can't show client-visible behavior), a hand-verified read of Go 1.26's actual `net/http/server.go:1720-1733` for K2 rather than trusting either review's paraphrase, and a panicking-`Logger` reproduction for K5 confirming which layer (svcerr's vs. `net/http`'s) actually contains the failure.
