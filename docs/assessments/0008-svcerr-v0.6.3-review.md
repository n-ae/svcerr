---
assessment: 0008
title: svcerr v0.6.3 review
date: 2026-07-17
status: Accepted
reviewer: maintainable-architect-v4
prior: 0007-svcerr-v0.6.2-review.md (v0.6.2)
---

# Assessment 0008: svcerr v0.6.3 review

## Executive summary

v0.6.3 (32311fd) closes assessment 0007's K1/K2/K3/K5 cleanly: a panic on an
already-committed response now re-panics with `http.ErrAbortHandler` instead
of returning normally (K1), `commitOnFlushError` marks the tracker committed
unconditionally before delegating, matching what the real `net/http`
`response.FlushError` does (K2), `WriteJSONResult`/`WriteHTMLResult`/
`WriteProblemResult` give pure callers the render/write errors the plain
`Write*` functions still discard (K3), and `safeLog` now contains a panicking
`Logger.Log` behind its own `recover()` (K5). `go test -count=1`,
`go test -race -count=1`, and `go vet` pass clean on both modules
(`GOWORK=off` and via the workspace); `golangci-lint run` reports 0 issues on
both; coverage is 99.7% root / 100.0% adapter via `go tool cover -func`.

This assessment was prompted by two inputs, from two genuinely different
sources this time - worth being precise about, since 0005/0007 both
mischaracterized repeated findings from the same recurring review as
independent corroboration (corrected in a4f32f5):

1. **`docs/repo-review-2026-07-17.md`**, a first-appearance review credited
   to "Codex" that reviewed `a4f32f5` directly against a real
   `httptest.Server`. Raises H1 (a `ResponseController`/`Unwrap` commit-
   tracking bypass), M1 (an invalid `WriteHeader` status is mis-recorded as
   committed), and three low-severity items (L1-L3 in its own numbering).
2. A further review in the same style, format, and recurring concerns as
   the "WDYT" source behind 0002-0004/0006/0007 (ratings table, "Verdict",
   "Priority for vX.Y.Z" section) - not attributed by name in this
   conversation, but its claims 3 and 4 below (`WWW-Authenticate` default,
   configurable header policy) are verbatim repeats of asks 0006 and 0007
   already evaluated and rejected as findings from that same source. Treated
   accordingly: a third restatement from one source is still one opinion,
   not three-way corroboration.

I reproduced every claim worth reproducing directly against this working
tree using throwaway `_test.go` files (not committed, removed after use),
not by trusting either review's prose. Both headline claims - the
`Unwrap`-mediated commit-tracking bypass and the log-metadata/response-
classification mismatch - reproduce exactly as described.

### What actually reproduces

| # | Source | Claim | Verdict |
|---|---|---|---|
| 1 | Codex H1 | `http.ResponseController` can flush through an `Unwrap`-only intermediate wrapper without updating the commit tracker, so a post-flush panic looks like a clean 200 | **Real**, reproduced via `httptest.Server`. See L1. |
| 2 | Codex M1 | An invalid `WriteHeader` status (e.g. 99) is recorded as committed before the real writer's `WriteHeader` panics on it | **Real**, reproduced against both a raw `net/http` server and through `RecoveryMiddleware`. See L2. |
| 3 | Verdict §1 | `errorLogFields` derives the status/code from the outermost coded node but independently re-scans the whole chain for type-specific fields, so logged metadata can describe a different node than the code | **Real**, reproduced. See L3. |
| 4 | Codex L1 | `writeHTMLErrorBody` never calls `rateLimitRetryAfterHeader`, so HTML 429 responses silently drop `Retry-After` that JSON/problem+json preserve | **Real**, confirmed by inspection. See L4. |
| 5 | Codex L2 | README still says a committed-response panic "just logs" (pre-v0.6.3 behavior) and its independent-reporter example doesn't mention the new `*Result` API | **Real**, confirmed by inspection. See L5. |
| 6 | Verdict §5 | `WriteResult.Status` is documented in a way that reads as transport-confirmed rather than merely the status svcerr selected | **Real, doc-precision only.** See L6. |
| 7 | Verdict §2 | A `Write` returning `n < len(p)` with a nil error (violating `io.Writer`'s contract) is silently treated as a full write | **Real but requires a non-conforming writer**, not a defect against any real `net/http` writer. Reproduced; treated as optional hardening, not a finding. |
| 8 | Codex L3 | The `runtime.Goexit` regression test is not an end-to-end substitute for `panic(nil)` - it doesn't reach `net/http`'s own `finishRequest` flush | **Accurate observation**, but the existing test's actual purpose (exercising the `rec == nil && !returnedNormally` branch) is still valid. Test-hygiene backlog item, not a defect. |
| 9 | Verdict §3 | 401 responses can still be sent with no `WWW-Authenticate` challenge if the caller never calls `SetAuthenticateChallenge` | **Not a defect** - same trade-off 0006 and 0007 already rejected, from the same recurring source, now for a third time. |
| 10 | Verdict §4 | Header cleanup (`prepareErrorHeaders`) is a fixed, unconfigurable policy | **Not a defect**, same accepted trade-off as 0007. See note under Accepted trade-offs on 0007 Recommendation 7's precondition. |

## Findings

### Major

**L1 - `http.ResponseController` bypasses commit tracking through an `Unwrap`-only intermediate wrapper (M).**

`newTrackingResponseWriter` (http.go:962-983) selects among `flushTracker`/
`hijackTracker`/`flushErrorTracker`/etc. by type-asserting `http.Flusher`,
`http.Hijacker`, and `flushErrorer` **only on the immediate `w`**. But the
base `trackingResponseWriter` unconditionally exposes that same immediate
`w` through `Unwrap()` (http.go:803-805), specifically so
`http.ResponseController` can reach deadline-related operations through a
wrapper. `ResponseController`'s own algorithm checks the current writer's
capabilities first, and only then follows `Unwrap()` to the next layer -
repeatedly, until it finds a match or runs out of layers.

That combination bypasses tracking whenever a real flusher sits behind an
intermediate wrapper that implements only `http.ResponseWriter` plus
`Unwrap() http.ResponseWriter` - a legitimate, commonly-used shape, since
`Unwrap` is exactly the mechanism `ResponseController` documents for
middleware to preserve controller operations through:

```go
type unwrapOnly struct{ http.ResponseWriter }

func (w *unwrapOnly) Unwrap() http.ResponseWriter { return w.ResponseWriter }
```

I reproduced this against a real `httptest.Server`:

```go
srv := httptest.NewServer(RecoveryMiddleware(nil)(http.HandlerFunc(
    func(w http.ResponseWriter, _ *http.Request) {
        wrapped := &unwrapOnly{ResponseWriter: w}
        _ = http.NewResponseController(wrapped).Flush()
        panic("boom after flush via unwrap-only wrapper")
    })))
```

Result: `status=200 body=""`. `newTrackingResponseWriter` sees no
`Flusher`/`FlushError`/`Hijacker` on the immediate `unwrapOnly` value and
returns the bare `trackingResponseWriter`, which has no `Flush` method of
its own. `ResponseController.Flush()` then walks past both the tracker and
`unwrapOnly` via `Unwrap()` and flushes the real server writer directly -
committing a 200 with an empty body without `tw.wroteHeader` ever being set.
The subsequent panic hits `RecoveryMiddleware`'s *uncommitted* branch,
which then gets a "superfluous response.WriteHeader" from `net/http` on its
own attempted 500 and treats the write as successful. This is the exact
failure mode v0.6.3's committed-response fix (K1 in 0007) was built to
prevent - reintroduced through a path that fix didn't cover.

Not a contrived wrapper: this is precisely the shape `http.ResponseController`
was designed to see through, and any middleware that wraps `ResponseWriter`
while wanting to stay compatible with `ResponseController` (deadline setting,
`http.Vary`-style helpers, several real-world logging/metrics middlewares)
has a reason to implement exactly this method set.

Recommended direction: make `newTrackingResponseWriter`'s capability
discovery walk the same `Unwrap` chain `ResponseController` walks, and
construct whichever tracker variant matches around the *discovered*
capability (however many layers down it is) rather than only the immediate
`w`. That's sufficient by itself: `ResponseController.Flush()` checks the
outermost writer's own methods before ever calling `Unwrap()`, so once the
returned wrapper itself exposes `Flush`/`FlushError`/`Hijack` (backed by the
real, possibly-nested capability), `ResponseController` matches at the first
layer and never reaches through to the raw writer. `trackingResponseWriter.Unwrap()`
should keep returning the immediate `w` unchanged - deadline-related
`ResponseController` operations still need to reach through it - only
capability *discovery* needs to look deeper. Add regression tests with an
`unwrapOnly` layer for `Flush`, `FlushError`, and `Hijack`, including at
least one real-server round trip for the flush-then-panic case, mirroring
K1's own test gap called out in 0007.

**L2 - `errorLogFields` can attribute logged metadata to a different node in the chain than the code/status it logs alongside (S).**

`extractErrorDetails` (http.go:592-644, used for the client-facing response
body) derives every type-specific field from a single node -
`outermostCoded(err)` - specifically to prevent an outer wrapper's code from
being paired with an inner error's details; its own doc comment says so
explicitly: *"an outer wrapper's code (e.g. `ErrCodeInternal` from
`WrapInternalError`) can end up paired with a wrapped error's details (e.g.
a `NotFoundError`'s resource ID), leaking structured data that the wrapping
was meant to hide."*

`errorLogFields` (http.go:650-696, used only for **logging**, not the
response body) doesn't follow that rule. It gets `code` from
`GetErrorCode(err)` (itself backed by `outermostCoded`), but then
independently re-walks the *entire* chain with five separate `errors.As`
calls in a fixed order - `*ValidationError`, `*DatabaseError`,
`*ExternalAPIError`, `*AuthenticationError`, `*NotFoundError` - and takes the
first match, regardless of whether that match is the same node the code
came from.

I reproduced this:

```go
inner := NewDatabaseError("query", "repository query failed")
outer := WrapNotFoundError(inner, "user", "123")

code := GetErrorCode(outer)                                    // NOT_FOUND
_, fields := errorLogFields(outer, HTTPStatusCode(code))        // 404
```

`fields` contained `db_operation: "query"` (from the *inner* `DatabaseError`,
which the `switch` reaches before it would reach `*NotFoundError`) and did
**not** contain `resource_type`/`resource_id` from the *outer* `NotFoundError`
that actually produced the `NOT_FOUND` code and 404 status. A log record for
this event reads `error_code=NOT_FOUND http_status=404 db_operation=query`
with no resource identifier at all - internally inconsistent, and exactly
the failure mode `outermostCoded` exists to prevent, two functions away from
where that lesson was already applied.

This doesn't affect the client-visible response - `extractErrorDetails`
already gets this right - only structured logging/observability. Recommended
fix: type-switch on `outermostCoded(err)` directly (as `extractErrorDetails`
already does), instead of independent `errors.As` calls:

```go
switch v := outermostCoded(err).(type) {
case *ValidationError:
    fields["field"] = v.Field
case *DatabaseError:
    fields["db_operation"] = v.Operation
case *ExternalAPIError:
    fields["service"] = v.Service
    fields["service_status"] = v.StatusCode
case *AuthenticationError:
    fields["auth_reason"] = v.Reason
case *NotFoundError:
    fields["resource_type"] = v.ResourceType
    fields["resource_id"] = v.ResourceID
}
```

Inner-cause context is still useful for debugging and can be added back
deliberately under distinct, clearly-inner-labeled keys (e.g.
`cause_db_operation`) rather than being silently substituted for the outer
node's own fields.

### Minor

**L3 - An invalid `WriteHeader` status is recorded as committed before the underlying writer validates it (S).**

`trackingResponseWriter.WriteHeader` (http.go:769-789) sets `wroteHeader`
and `status` **before** delegating to `w.ResponseWriter.WriteHeader(status)`.
The real `net/http` server (and any writer with equivalent validation)
panics on a status outside its accepted range - I confirmed this directly
against a raw `net/http` server (not `httptest.Recorder`, which is lenient):
`w.WriteHeader(99)` panics server-side with `invalid WriteHeader code 99`
before anything reaches the wire.

Because `trackingResponseWriter` records the commit *before* that panic,
`RecoveryMiddleware`'s deferred function sees `tw.wroteHeader == true` and
takes the already-committed branch - logging and re-panicking with
`http.ErrAbortHandler` - instead of the uncommitted branch, which would
otherwise still be able to write a real, valid 500 JSON response (nothing
had actually reached the connection yet). I reproduced the end-to-end
consequence through `RecoveryMiddleware` on a real `httptest.Server`: the
client gets a bare `EOF` instead of the diagnosable 500 body a genuinely
uncommitted response would produce.

This only triggers on a status code outside net/http's valid range, which
svcerr's own mappings can't produce (`RegisterStatusCode` already validates
400-599) - it requires a wrapped application handler calling
`w.WriteHeader` with a bad literal value directly. Low likelihood, but the
fix is cheap and turns a raw connection abort back into a diagnosable
response: validate the status (or record commitment only after the
delegate call returns without panicking) before mutating tracker state.

**L4 - HTML rate-limit responses silently drop `Retry-After` (S).**

`prepareErrorHeaders` deletes any pre-existing `Retry-After`
(http.go:243). `writeJSONErrorBody` and `writeProblemJSONBody` both re-add
it via `rateLimitRetryAfterHeader` when the outermost coded node is a
`*RateLimitError` (http.go:196-198, 444-446). `writeHTMLErrorBody`
(http.go:316-333) has no equivalent call - confirmed by inspection, no
`rateLimitRetryAfterHeader` reference anywhere in that function.

`svcerr.WriteHTML(w, svcerr.NewRateLimitError("api", 10, 60))` sends a 429
with no `Retry-After` header at all, while the JSON and problem+json writers
for the identical error both send one. `Retry-After` is optional on a 429,
so this isn't a protocol violation, just an inconsistency between the three
renderers for data the caller explicitly supplied. One-line fix: call
`rateLimitRetryAfterHeader(w.Header(), outermostCoded(err))` in
`writeHTMLErrorBody` before `WriteHeader`, matching the other two writers.

**L5 - README describes pre-v0.6.3 recovery behavior and omits the `*Result` API from its reporter example (S, doc-only).**

Two sections have drifted from v0.6.3's actual behavior, confirmed by
reading the current README directly:

1. `README.md:126-129`: *"won't write an error body over one that's already
   committed - it just logs in that case."* That was true through v0.6.2;
   v0.6.3's K1 fix (this is what "Recovery behavior is now correct" in the
   Verdict input is describing) means it now logs **and** re-panics with
   `http.ErrAbortHandler`, closing the HTTP/1 connection or resetting the
   HTTP/2 stream. That's material, client-visible transport behavior, not
   an internal implementation detail.
2. `README.md:424-433`: the independent-reporter example still calls plain
   `WriteJSON` and passes only its returned status to the reporter -
   `WriteJSONResult`/`WriteHTMLResult`/`WriteProblemResult` exist for
   exactly this use case (a caller with its own error-reporting path, which
   is the stated audience for that whole README section) but aren't
   mentioned.

**L6 - `WriteResult.Status`'s doc comment reads as transport-confirmed rather than merely selected (S, doc-only).**

The current doc comment (http.go:117-119) says *"Status is the HTTP status
code actually sent."* That's accurate for every writer this package's own
tests exercise, but not guaranteed in general: once svcerr calls
`w.WriteHeader(status)`, a custom or third-party `ResponseWriter` could
ignore it, have already committed a different status earlier, transform it,
or panic during the call (see L3). No response-writing library can recover
the client-observed status from an arbitrary wrapper unless that wrapper
explicitly exposes it. Reword to something like *"Status is the status code
svcerr selected and passed to `WriteHeader` - not a transport confirmation
that the client received exactly that status."* Doc-only; no behavior
change.

### Hardening (not findings)

**A `Write` returning a short count with a nil error is accepted as a full write.**

`writeJSONErrorBody`/`writeHTMLErrorBody`/`writeProblemJSONBody`/`RecoveryMiddleware`
all do `_, writeErr = w.Write(body)`, discarding the returned byte count. I
confirmed a `Write` that returns `len(p)/2, nil` (violating `io.Writer`'s
documented contract - "`Write` must return a non-nil error if it returns
`n < len(p)`") produces a `WriteResult` with `WriteErr == nil` despite only
half the body reaching the writer. Every real `net/http`-backed writer
already honors the contract, so this isn't a defect against realistic
usage - it's hardening against a non-conforming custom writer, test double,
or future adapter. Cheap to add if it's ever prioritized:

```go
n, writeErr := w.Write(body)
if writeErr == nil && n != len(body) {
    writeErr = io.ErrShortWrite
}
```

Applying this consistently across all four write sites would make the
package robust against a specific, narrow category of buggy wrappers, not
against anything a spec-conforming writer can do.

## Accepted trade-offs (not findings)

- **401 responses have no default `WWW-Authenticate` challenge.** Same
  trade-off 0006 and 0007 both already rejected as a finding. This is the
  same recurring review source asking a third time, not new corroboration -
  0006's stated condition for building this ("real usage shows callers
  routinely forgetting it") still isn't met by repetition from one source.
- **Header cleanup (`prepareErrorHeaders`) is a fixed, unconfigurable
  policy.** Same trade-off as 0007's K4/Recommendation 7. 0007 conditioned
  evaluating a `HeaderPolicy` split on "if K1/K2 land first and
  streaming/compression callers remain the primary friction point." K1/K2
  did land this release, but the second half of that condition - actual
  reported friction from a compression/streaming caller, as opposed to a
  review asking for it again - still isn't in evidence. Worth revisiting if
  and when that shows up; not worth building speculatively now.
- **`runtime.Goexit` as a `panic(nil)` regression-test surrogate.** Codex L3
  is an accurate observation - `Goexit` doesn't reach `net/http`'s
  `finishRequest` flush the way a real connection would, so the existing
  test is equivalent only at the `recover()`-return-value level. The test
  still correctly exercises the branch it was written for
  (`rec == nil && !returnedNormally`); this is a coverage-completeness
  backlog item (add a real-server `Goexit` round trip, and/or a
  `GODEBUG=panicnil=1` `panic(nil)` test), not a defect in what's there.
- All trade-offs restated through 0007 (Flusher/Hijacker/FlushError-only
  preserved interfaces - `http.Pusher` and `io.ReaderFrom` are still
  dropped by every tracker variant, opt-out structured details, shallow
  `Context()`, non-concurrent-safe error mutation, generic custom-code
  messages) still hold and were not re-counted here.

## Recommendations

**This week:**
1. Fix L1: make commit-tracking capability discovery in
   `newTrackingResponseWriter` follow the same `Unwrap` chain
   `http.ResponseController` follows, and construct the tracker variant
   around whatever capability is actually discovered (however many layers
   down). Add `unwrapOnly`-wrapped regression tests for `Flush`,
   `FlushError`, and `Hijack`, at least one through a real server.
2. Fix L2: switch `errorLogFields` to a type-switch on `outermostCoded(err)`,
   matching `extractErrorDetails`'s existing pattern, instead of independent
   `errors.As` calls across the whole chain.

**This month:**
3. Fix L3: don't mark `trackingResponseWriter` committed until the
   delegate `WriteHeader` call actually returns (or validate the status
   first) - an invalid status shouldn't turn a recoverable request into a
   raw connection abort.
4. Fix L4: call `rateLimitRetryAfterHeader` from `writeHTMLErrorBody`.
5. Fix L5: update both README sections for v0.6.3's actual recovery
   behavior and the `*Result` API.
6. Fix L6: reword `WriteResult.Status`'s doc comment.

**This quarter (optional):**
7. Apply the short-write hardening (`io.ErrShortWrite` on `n != len(body)`)
   across all four write sites, if a use case with a non-conforming writer
   actually shows up.
8. Add a real-server `runtime.Goexit` round trip and/or a
   `GODEBUG=panicnil=1` `panic(nil)` test, per Codex L3.

## Verification performed

Against this working tree (`main` at `a4f32f5902afe535be39dd2d06f739032df6feae`,
tag `v0.6.3-1-ga4f32f5`, clean working tree aside from the untracked
`docs/repo-review-2026-07-17.md` this assessment reviews), using Go 1.26.5:

- `GOWORK=off go test -count=1 ./...`: pass, root module.
- `GOWORK=off go test -race -count=1 ./...`: pass, root module.
- `GOWORK=off go vet ./...`: clean, root module.
- `go vet ./...`, `go test -count=1 ./...`, `go test -race -count=1 ./...`:
  pass, zerologadapter module.
- `gofmt -l .`: no output.
- `golangci-lint run ./...` (v2.12.2): 0 issues, both modules.
- Coverage via `go tool cover -func`: 99.7% root, 100.0% zerologadapter.

L1, L2, L3, and the short-write hardening claim were reproduced with
throwaway `_test.go` files added to and removed from the root package (not
committed): a real `httptest.Server` round trip for L1 (an `unwrapOnly`
wrapper around the server's real writer, flush-then-panic), a direct call
into `errorLogFields` for L2's log-metadata mismatch, and a raw `net/http`
server plus a `RecoveryMiddleware`-wrapped `httptest.Server` for L3
(confirming both the stdlib's actual panic-on-invalid-status behavior and
the resulting client-side `EOF`). L4, L5, and L6 were confirmed by direct
inspection of `http.go` and `README.md` rather than reproduction, since
they're either a missing function call, a stale prose claim, or a
doc-comment wording issue with no runtime behavior to exercise.
