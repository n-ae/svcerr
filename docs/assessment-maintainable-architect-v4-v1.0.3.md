---
title: Maintainable Architect Assessment — v4
version_reviewed: v1.0.3 + 1 commit (root module HEAD; adapter requirement bump, untagged)
date: 2026-07-18
reviewer: maintainable-architect-v4
status: Accepted
---

# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v1.0.3 (tag `v1.0.3` = `a06ef7a`) plus one untagged commit, HEAD `171d77a` ("Bump zerologadapter's svcerr requirement to v1.0.3")
**Date:** 2026-07-18
**Reviewer:** maintainable-architect-v4
**Prior assessment (this series):** [docs/assessment-maintainable-architect-v4-v1.0.2.md](assessment-maintainable-architect-v4-v1.0.2.md) (reconciled two disagreeing Codex reviews; opened M1/L1/L2 for v1.0.3)
**Cross-review inputs:** [docs/current-repository-assessment-2026-07-18.md](current-repository-assessment-2026-07-18.md) (Codex, direct review of HEAD `171d77a`, "Input 1", one medium finding); a second, independent third-party review of the same HEAD supplied by the maintainer in chat, not saved to disk ("Input 2", three findings plus a rating table)

---

## Disclosure

Two inputs this cycle, same as last: one filed to disk by a direct Codex
review, one pasted in chat. Unlike last cycle they mostly agree rather than
disagree — Input 1's only finding is a strict subset of Input 2's finding #2,
described independently in different words. That agreement is itself not
proof; it's proof that the same gap is easy to spot from the same starting
point (an external `Coder`-only type), not that either input's account of
severity, scope, or root cause is correct. Every reproducible claim in both
inputs was rebuilt fresh against actual HEAD source with throwaway Go
programs, run in a scratch module (`replace github.com/n-ae/svcerr =>
<repo>`) outside the repository and deleted after use — `git status` shows
only the pre-existing untracked Input 1 file, nothing added by this review.

## Verdict

**v1.0.3 correctly closed both actionable items from the v1.0.2 review** —
`GetErrorCode`/`outermostCoded` no longer panics on a typed-nil pointer-backed
coded error, and an empty external `Coder.Code()` is normalized at the one
extraction point every renderer shares. Both are independently reproduced
below as fixed. **But the fix is narrower than it reads**, in two directions
neither v1.0.2 nor v1.0.3's own commit message called out: the *classification*
path (`GetErrorCode`, and by extension every response writer) is now
panic-safe for a typed-nil coded error, while the separate *stack-trace*
path (`GetStackTrace`, `RecaptureStackTrace`) has no such guard at all and
still panics on the exact same common case — a plain `*NotFoundError` or
`*BaseError` nil pointer, not an exotic type. That gap is reachable through
`WriteHTTPError`/`WriteHTTPProblem` for any 5xx-classified typed-nil error,
including with a nil logger (documented as tolerated), and demonstrably turns
an already-written, valid 500 response into a client-visible connection reset
when the handler is wrapped in `RecoveryMiddleware` — verified below with a
real `httptest.Server`, not just a unit-level panic/recover. Separately, the
narrower pointer-only shape of `isNilValue` that v1.0.2 accepted is still
exactly that: a genuine, corroborated (not new) gap for non-pointer bare
`Coder` types.

| Severity | Count | Summary |
|---|---:|---|
| Critical / high | 0 | — |
| Medium | 1 | M1: `GetStackTrace`/`RecaptureStackTrace` have no typed-nil guard at all and panic on the same pointer-backed typed-nil error that `GetErrorCode` already handles safely; reachable through the logging path of every `WriteHTTPError`/`WriteHTTPProblem` call, nil logger or not, and demonstrated to abort an already-good in-flight response when `RecoveryMiddleware` is installed |
| Low | 1 | L1: `isNilValue` recognizes only `reflect.Pointer`; a nil-backed `Coder` of slice/map/func/chan/interface kind still panics classification (Input 1's M1 and Input 2's finding #2, corroborating one gap, not two) |
| Note, not a finding | 1 | N1: `errors.Join` with a typed-nil first coded match classifies the whole tree as no-match rather than continuing to a later valid node — accurate, conservative, and already implied by the existing doc comment's traversal-order note, but not spelled out for this specific case |
| Confirmed unchanged (carried over) | 2 | Context-free `Logger` (ADR 0001) and marshal-panic stack provenance (ADR 0002) — both re-confirmed present and still correctly dispositioned as declined-by-design, not re-opened |

## Reconciling Input 1 and Input 2

Input 1 is a single, precisely-scoped finding: `isNilValue` (`errors.go:1107`)
checks only `rv.Kind() == reflect.Pointer && rv.IsNil()`, so a bare `Coder`
implemented by a named slice/map/func/chan/interface type — which the
package's own doc comment at the top of the error-checking section
advertises as a supported extension shape, not requiring `BaseError`
embedding — can still carry a nil concrete value past `outermostCoded`'s
guard and panic inside `Code()`. This is real and reproduced below.

Input 2 makes the same observation as its finding #2 (verbatim: "`isNilValue`
recognizes only pointers"), so these are one gap independently described
twice, not two — the task framing that asked to treat them as corroborating
rather than double-counted is followed here as L1.

Where Input 2 goes further, and where Input 1's scope (limited to
`isNilValue`/`outermostCoded`) doesn't reach, is finding #1: the claim that
*stack-trace extraction* is a separate code path from *code classification*,
and that v1.0.3 only hardened the latter. That distinction is correct and is
this cycle's most consequential finding — promoted to this review's M1,
since it's reachable for the ordinary pointer case v1.0.2 already fixed for
classification, not just the exotic non-pointer case L1 covers. Input 2's
finding #3 (the `errors.Join` conservative-classification behavior) has no
counterpart in Input 1 at all; verified below and kept at note-only severity,
matching Input 2's own characterization ("a safe, conservative result, not a
response-safety defect").

## Claim-by-claim verification

All claims reproduced fresh against `171d77a`, Go 1.26.5, darwin/arm64, via a
throwaway module (`replace github.com/n-ae/svcerr => <repo>`) built outside
the repository and removed after use.

| # | Claim (source) | Verdict | Evidence |
|---|---|---|---|
| 1 | `GetErrorCode` no longer panics on typed-nil pointer-backed coded errors (v1.0.3 release claim, both inputs) | **Confirmed fixed** | `var nf *svcerr.NotFoundError; var err error = nf; svcerr.GetErrorCode(err)` returns `INTERNAL_ERROR`, no panic. |
| 2 | `GetStackTrace` still panics on the same typed-nil pointer error (Input 2, finding #1) | **Confirmed** | Same `err` value: `svcerr.GetStackTrace(err)` panics with `runtime error: invalid memory address or nil pointer dereference`. |
| 3 | `RecaptureStackTrace` has the same gap (Input 2, finding #1) | **Confirmed** | `svcerr.RecaptureStackTrace(err, 0)` panics identically; `errors.go:512`'s `errors.As(err, &setter)` succeeds and `setter.setStackTrace(...)` is called with no nil check in between. |
| 4 | `WriteHTTPError(w, err, nil)` panics during logging even though the JSON body was already written, despite nil-logger being documented as tolerated (Input 2, finding #1) | **Confirmed** | `httptest.NewRecorder()` shows `w.Code == 500` and a complete, correct `{"error":{"code":"INTERNAL_ERROR",...}}` body already recorded at the moment the subsequent panic is caught — the panic fires inside `logError`→`errorLogFields`→`GetStackTrace`, after `writeJSONErrorBody` returned. |
| 5 | Same panic occurs with a non-nil logger too (Input 2, finding #1, implied) | **Confirmed** | Identical panic with a working `Logger` supplied; the logger's `Log` is never reached because the panic fires inside `errorLogFields`, before `safeLog` calls into it. |
| 6 | `RecoveryMiddleware` demotes this to `http.ErrAbortHandler`, discarding the already-good response (Input 2, finding #1) | **Confirmed against a real server** | A handler calling `WriteHTTPError(w, typedNilErr, nil)` behind `svcerr.RecoveryMiddleware(nil)`, served via `httptest.NewServer`, produces a client-visible `Get ...: EOF` — the valid 500 body written server-side never reaches the client because `tw.wroteHeader` is already true when the second panic is recovered, so `recovery.go:113` fires `panic(http.ErrAbortHandler)` instead of letting the response stand. |
| 7 | `isNilValue` only covers `reflect.Pointer`; a nil-backed slice/map/func/chan/interface `Coder` still panics (Input 1 M1; Input 2 finding #2) | **Confirmed** | A `type sliceCoder []string` with `Code()`/`Error()` methods, assigned `nil` and boxed in `error`, panics `svcerr.GetErrorCode(err)` with `index out of range [0] with length 0` — a different panic message than the pointer case, same root cause (`isNilValue` returns `false` for a nil slice, so `outermostCoded` treats it as a genuine match). |
| 8 | `errors.Join` of a typed-nil coded error followed by a valid one classifies the whole tree as `INTERNAL_ERROR`, not the valid node (Input 2, finding #3) | **Confirmed, and correctly characterized as safe, not a defect** | `errors.Join(typedNilErr, svcerr.NewNotFoundError("thing", "id-1"))` classifies as `ErrCodeInternal`, not `ErrCodeNotFound` — `outermostCoded` performs one `errors.As`, finds the typed-nil node first (pre-order, depth-first), rejects it via `isNilValue`, and returns `nil` without continuing the search. Conservative (falls back to "no match" rather than exposing a fabricated worse-or-better classification), consistent with the package's stated position that callers aggregating differently-severe errors should classify explicitly rather than rely on traversal order. |
| 9 | Two v1.0.2-cycle design decisions (context-free `Logger`, marshal-panic stack provenance) remain unchanged and documented (both inputs, "accepted design decisions") | **Confirmed, not new** | `docs/adr/0001-logger-has-no-context-parameter.md` and `docs/adr/0002-marshal-panic-log-keeps-the-original-errors-stack.md` exist and match; `logger.go`'s `Log` signature is unchanged; `logging.go`'s `errorLogFields`/`logError` still derive `stack_trace` from the original error, not a marshal-panic site. |

### M1: stack-trace extraction has no typed-nil guard, unlike code classification (claims 2–6)

Real, and more consequential than either input alone frames it, because it
undoes part of v1.0.3's own fix for the common case. `outermostCoded`
(`errors.go:1089`) gained `!isNilValue(c)` in v1.0.3, and `GetErrorCode`
(`errors.go:1124`) calls only `outermostCoded` — so classification is now
correctly panic-safe for a typed-nil `*BaseError` or `*NotFoundError`,
exactly the case v1.0.2's M1 described. But `GetStackTrace` (`errors.go:1133`)
and `RecaptureStackTrace` (`errors.go:507`) each run their own,
*independent* `errors.As` search for a different target interface
(`StackTracer`, `stackTraceSetter`) and call straight into the result with no
nil check at all — not a narrower version of the same guard, no guard:

```go
func GetStackTrace(err error) []string {
	var st StackTracer
	if errors.As(err, &st) {
		return st.StackTrace()
	}
	return nil
}
```

The consequence is concrete, not theoretical, and was verified against a
running `net/http` server rather than only a recover/panic unit check:
`errorLogFields` (`logging.go:24-28`) calls `GetStackTrace(err)` whenever the
selected status is 5xx, and `logError` (`logging.go:79-93`) builds those
fields *before* `safeLog` (`logging.go:116-122`) checks `logger == nil` — so
the crash happens regardless of whether a logger was even supplied, which
matters because the package's own documentation for `safeLog` explicitly
promises a nil logger is tolerated everywhere logging happens. Because
`writeJSONErrorBody` already ran and wrote a complete, correct 500 body to
the `ResponseWriter` before `logError` is called, a direct
`WriteHTTPError`/`WriteHTTPProblem` call without an enclosing recovery layer
turns a successfully-rendered response into an unrecovered panic in that
request. Worse, wrapping the same handler in the officially recommended
`RecoveryMiddleware` does not save the response either: since
`tw.wroteHeader` is already `true` by the time the second panic is caught,
`recoveryMiddleware`'s defer treats it as "panic after response already
committed" (`recovery.go:62-114`) and calls `panic(http.ErrAbortHandler)`
(`recovery.go:113`) instead of letting the already-correct response stand.
Reproduced end-to-end with a real `httptest.Server`: the client's
`http.Get` returns `EOF`, not the 500 JSON body the server actually sent.
The one guarantee `RecoveryMiddleware` exists to provide — a committed
response is never worse than what the handler already wrote — is exactly
what a typed-nil error, the single most common accidental non-nil `error`
value in Go, defeats here.

**Worth fixing, and at higher priority than L1.** L1 requires a caller to
hand-write a non-pointer `Coder`, a shape no built-in error type in this
package uses and few third parties will reach for. M1 requires only a
function declared to return a concrete pointer type (`func Lookup(id string)
(*svcerr.NotFoundError, bool)`, say) that returns `nil` on a not-found path
and gets assigned to a plain `error` before reaching a writer — ordinary,
unremarkable Go, and the exact case `outermostCoded` was already fixed for
everywhere else. Fix at the same two choke points, mirroring the pattern
`outermostCoded` already established: check `!errors.As(err, &st) ||
isNilValue(st)` in `GetStackTrace`, and `!errors.As(err, &setter) ||
isNilValue(setter)` in `RecaptureStackTrace`, both returning their existing
"not found" result (`nil` / a no-op) rather than proceeding to call the
method. As a second, independent line of defense worth taking regardless,
`logError` should check `logger == nil` and return before calling
`errorLogFields` at all, matching the early-return `safeLog` and (per Input
2) `Renderer.log` already use — the nil-logger case should never build
diagnostics it's about to throw away, and doing so removes one whole trigger
path even before the `GetStackTrace` guard lands.

### L1: `isNilValue` recognizes only pointers (Input 1's M1; Input 2's finding #2 — one gap)

Real, corroborated independently by both inputs, and unchanged from how
Input 1 already characterizes it — nothing in this review's own
verification adds scope beyond what Input 1 already scoped precisely.
`isNilValue` (`errors.go:1107`) is `rv.Kind() == reflect.Pointer &&
rv.IsNil()`; a named slice/map/func/chan/interface type implementing only
`Coder` (the package's doc comment for the error-checking section explicitly
supports this: "a custom error type that implements just `Code() ErrorCode`
is picked up here... even if it doesn't also implement `Unwrap` or
`StackTrace`") can carry a nil concrete value through `outermostCoded`
undetected. Confirmed with a nil-valued named slice type: `GetErrorCode`
panics with `index out of range [0] with length 0` rather than returning
`ErrCodeInternal`.

**Worth fixing, at low priority**, same disposition as Input 1's own
recommendation: extend `isNilValue` to switch over `Chan`, `Func`,
`Interface`, `Map`, `Pointer`, and `Slice` kinds (returning `IsNil()` for
each, `false` otherwise), and update the doc comment, which currently
asserts "no error type in practice uses" the non-pointer kinds — an
assertion this finding falsifies for a legitimately supported extension
point even if an unlikely one in practice.

### N1: `errors.Join`'s conservative classification when the first coded match is typed-nil — documented, not a defect (claim 8)

Confirmed as Input 2 describes it, and correctly rated by Input 2 as
"a safe, conservative result, not a response-safety defect" rather than a
new severity tier. `outermostCoded` performs exactly one `errors.As`, which
for a joined error returns the first match in pre-order, depth-first
traversal; if that match is typed-nil, `isNilValue` correctly rejects it,
but the function then returns `nil` for the whole call rather than
continuing to search siblings or later branches for a valid coded node.
Reproduced: `errors.Join(typedNilErr, NewNotFoundError(...))` classifies as
`ErrCodeInternal`, not `ErrCodeNotFound`. The existing doc comment on
`outermostCoded` already tells callers aggregating errors of different
severities to classify explicitly rather than rely on traversal order — this
finding is a specific instance of that general note, not a new behavior, and
implementing tree-continuation search for one specific rejection reason
(typed-nil) while not for any other would be inconsistent with "first match
wins" as the function's one documented rule. **Documented here, not
recommended for a code change** — matching Input 2's own recommendation to
document rather than build custom traversal for a case with no reported
real-world trigger.

### Confirmed unchanged: ADR 0001 (context-free Logger) and ADR 0002 (marshal-panic stack provenance)

Both inputs list these as "accepted design decisions, not new findings."
Confirmed present and unchanged: `docs/adr/0001-logger-has-no-context-parameter.md`
and `docs/adr/0002-marshal-panic-log-keeps-the-original-errors-stack.md`
both exist, `logger.go`'s `Logger.Log` signature still takes no
`context.Context`, and `logging.go` still derives `stack_trace` from the
original application error rather than a marshal-panic-site stack. No action
this cycle.

## Verification performed

At HEAD `171d77a`, Go 1.26.5, darwin/arm64, both modules:

- `go test -count=1 -cover ./...` — root 100.0%, adapter 100.0%
- `go test -race -count=1 ./...` — clean, both modules
- `go test -shuffle=on -count=20 ./...` — clean (root)
- `GODEBUG=panicnil=1 go test -count=1 ./...` — clean (root)
- `go vet ./...`, `gofmt -l .`, `go build ./...` — clean, both modules
- `GOWORK=off go mod tidy -diff` — clean, both modules
- `govulncheck ./...` — no vulnerabilities found (root)
- `git diff --check` — clean
- Nine fresh adversarial/reproduction probes against a throwaway module
  (`replace github.com/n-ae/svcerr => <repo>`), covering every claim in the
  table above, including one driven through a real `httptest.NewServer` (not
  just an in-process panic/recover) to confirm the client-visible EOF under
  `RecoveryMiddleware`; all deleted after use — `git status --short` shows
  only the pre-existing untracked `docs/current-repository-assessment-2026-07-18.md`
  input file, nothing added by this review
- Both inputs' own verification (Input 1: full matrix per its "Verification"
  section; Input 2: `-cover`, `-race`, `-shuffle=on -count=20`, `go vet`, both
  claimed against Go 1.23.2) accepted as corroborating for what they cover,
  not substituted for the fresh reproductions above, which neither input's
  own text shows a program for

## Revised rating

| Area | v1.0.2 | v1.0.3 (this review) |
|---|---|---|
| Problem selection | 9.0/10 | 9.0/10 |
| Scope and usability | 9.8/10 | 9.8/10 |
| Go idioms | 9.6/10 | 9.7/10 |
| Extensibility | 9.6/10 | 9.7/10 |
| Response safety | 9.5/10 | 9.6/10 |
| Middleware transparency | 9.8/10 | 9.5/10 |
| Production readiness | 9.6/10 | 9.6/10 |

Go idioms and Extensibility both tick up a notch, not to full marks: the
core choke point every renderer and classifier shares (`outermostCoded`) is
now correctly guarded against the textbook Go typed-nil-interface footgun
for the pointer case, which is real, measurable progress from v1.0.2 — but
the same footgun still reaches two adjacent public functions
(`GetStackTrace`, `RecaptureStackTrace`) that weren't touched, so the
underlying idiom gap is narrower, not closed. Response safety ticks up for
the same partial reason: the path every response body's *content* comes from
is now safe; the path its *logging* comes from is not. Middleware
transparency is this cycle's real casualty, dropping from v1.0.2's 9.8:
`RecoveryMiddleware`'s specific, documented guarantee — that a panic after
response commitment gets a clean abort rather than a corrupted 200 — is
sound in general, but is triggered here by the middleware's own logging call
into a function this same package left unguarded, discarding an
already-correct response the middleware had no reason to discard. Production
readiness holds flat: two real, narrow-trigger issues closed, one adjacent
one newly surfaced at comparable severity, netting to no change.

## v1.0.4 priorities

1. **M1** — guard `GetStackTrace` (`errors.go:1133`) and
   `RecaptureStackTrace` (`errors.go:507`) with the same `isNilValue` check
   `outermostCoded` already uses, treating a typed-nil match as "not found"
   (nil slice / no-op respectively) rather than calling the method; make
   `logError` (`logging.go:79`) return immediately when `logger == nil`,
   before calling `errorLogFields`, matching the early-return pattern
   `safeLog` already uses one level down. Regression tests for a typed-nil
   `*BaseError` and `*NotFoundError` through `GetStackTrace`,
   `RecaptureStackTrace`, and `WriteHTTPError`/`WriteHTTPProblem` in both a
   bare call and a `RecoveryMiddleware`-wrapped call (the latter ideally
   through a real `httptest.Server`, since the defect only manifests as a
   dropped response at the transport level, not as a panic message alone).
2. **L1** — extend `isNilValue` (`errors.go:1107`) to switch over `Chan`,
   `Func`, `Interface`, `Map`, `Pointer`, `Slice` kinds; update the kinds
   this covers in its own doc comment. Regression test with a nil-valued
   named slice or map `Coder`-only type through `GetErrorCode`.
3. **N1** — one sentence on `outermostCoded`'s existing doc comment noting
   that a typed-nil first match ends the search entirely (equivalent to no
   coded node anywhere in the tree), not just a skip of that one node. No
   code change.
4. ADR 0001 and ADR 0002: no action, both re-confirmed present and correctly
   dispositioned, not re-opened.

All three actionable items are non-breaking: M1 and L1 only narrow what a
panic or a wire-visible empty/incorrect value is allowed to do, and N1 is
documentation only.
