---
title: Maintainable Architect Assessment — v4
version_reviewed: v1.0.4 (tag `v1.0.4` = `86f8ee6`), root module HEAD `fe5a7af`
date: 2026-07-18
reviewer: maintainable-architect-v4
status: Accepted
---

# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v1.0.4 (tag `86f8ee6` for the root module fix; HEAD `fe5a7af` includes two follow-up docs/adapter-bump commits with no source changes)
**Date:** 2026-07-18
**Reviewer:** maintainable-architect-v4
**Prior assessment (this series):** [docs/assessment-maintainable-architect-v4-v1.0.3.md](assessment-maintainable-architect-v4-v1.0.3.md) (reconciled two disagreeing reviews of HEAD `171d77a`; opened M1 — no typed-nil guard on `GetStackTrace`/`RecaptureStackTrace` — and carried forward L1 — `isNilValue` is pointer-only)
**Cross-review inputs:** [docs/repository-assessment-v1.0.4-codex-2026-07-18.md](repository-assessment-v1.0.4-codex-2026-07-18.md) ("Source A", Codex, direct review of HEAD `fe5a7af`, one medium finding, disputes the assessment index's "M1 closed" claim); a second, independent third-party review of v1.0.4 supplied by the maintainer in chat, not saved to disk ("Source B", zero critical/high/medium findings, one low finding, one documentation note)

---

## Disclosure

Two inputs this cycle, same pattern as 0017 and 0018: both examined the same
release and reproduced the same underlying technical fact (`isNilValue` is
still `reflect.Pointer`-only, and a non-pointer nil-capable `Coder`/
`StackTracer` still panics), then disagreed on what that fact means —
severity (medium vs. low) and, more consequentially, whether it is the same
finding the maintainer's own v1.0.4 tag message claims to have closed.
Agreement on the reproduction is not proof either source's severity or
framing is correct; it just means the gap is easy to find from the same
starting point. Every reproducible claim in both inputs was rebuilt fresh
against actual HEAD `fe5a7af` source — reading `errors.go` and `logging.go`
directly, then running throwaway Go programs against a scratch module
(`replace github.com/n-ae/svcerr => <repo>`) outside the repository, deleted
after use. `git status --short` shows only the pre-existing untracked Source
A file; nothing added by this review.

## Verdict

**v1.0.4 does exactly what its own tag message says it does, and no more.**
The tag reads: "Closes the v1.0.3 assessment's M1 finding" — and 0018
defined M1 precisely as the missing typed-nil guard on `GetStackTrace` and
`RecaptureStackTrace`, plus `logError` building log fields before checking
for a nil logger. All three are confirmed fixed below, including an
end-to-end real-server reproduction showing `RecoveryMiddleware` no longer
discards an already-good response for that case. 0018's **separate**, lower
priority L1 — `isNilValue` recognizing only `reflect.Pointer`, so a
non-pointer nil-capable `Coder`/`StackTracer` (a named nil slice/map/func/
chan/interface) still panics — was never in scope for this release, is
unchanged, and remains open. It was already rated Low in 0018 and stays Low
here, for a reason neither source tests directly: unlike the closed M1, this
panic fires inside `GetErrorCode` at the very start of response
construction, before any header or body is written — confirmed below with a
real server — so `RecoveryMiddleware` recovers cleanly and the client still
gets a 500, rather than the connection-reset failure mode the closed M1
caused.

Source A is right about the technical fact and wrong about the label: it
calls the still-open `isNilValue` gap "M1," which conflates it with 0018's
actual M1 (closed) and produces the mistaken conclusion that the assessment
index's "M1 closed in v1.0.4" claim is premature. That claim is correct as
written — it refers to 0018's M1, not to 0018's L1. Source B's framing (the
v1.0.3-era robustness defect is closed; the `isNilValue` gap is a distinct,
low-severity residual) matches the evidence and the tag message. Source B's
severity call is adopted; Source A's medium rating is not.

| Severity | Count | Summary |
|---|---:|---|
| Critical / high | 0 | — |
| Medium | 0 | 0018's M1 (`GetStackTrace`/`RecaptureStackTrace`/`logError` typed-nil handling) is confirmed closed. |
| Low | 0 | L1 (carried over from 0018) is closed as of commit `1e8c547` — see [Addendum](#addendum-2026-07-19--l1-closed) below. Open at the time this assessment was first written; the table above and the body text that follows describe that original, point-in-time finding. |
| Note, not a finding | 1 | The `isNilValue` doc comment's claim that "no error type in practice uses" map/chan/func kinds is now demonstrably false (this review's own slice repro disproves it for slice, and the same reflect gap applies to map/chan/func/interface); worth a one-line comment correction regardless of whether the code changes. Corrected as part of the L1 closure below. |
| Confirmed unchanged (carried over) | 2 | Context-free `Logger` (ADR 0001) and marshal-panic stack provenance (ADR 0002) — both re-confirmed present and correctly dispositioned as declined-by-design. |

## Reconciling Source A and Source B

Both sources independently rediscovered the identical reflect gap and the
identical repro shape (a nil-valued non-pointer type implementing `Coder`).
Where they diverge is scope and severity, and both divergences are
resolvable against 0018's own text and the v1.0.4 tag message rather than
against either source's own framing.

**Scope.** Source A's finding is titled "M1 — `isNilValue` excludes
typed-nil custom `Coder` values other than pointers" and its verdict states
"the assessment-index claim that M1 is closed is premature." But 0018 — the
assessment the index row and the v1.0.4 tag both refer to — used "M1" for a
different, already-fixed defect (no guard at all on `GetStackTrace`/
`RecaptureStackTrace`, reachable through ordinary pointer-shaped errors, and
demonstrated to abort an already-good response under `RecoveryMiddleware`)
and used "L1" for exactly the pointer-vs-non-pointer gap Source A is
describing. Source A's own reproduction (`codedSlice`, a nil-valued named
slice) is 0018's L1 verbatim, not 0018's M1. Re-verified directly against
`errors.go`: `GetStackTrace` (line 1139) and `RecaptureStackTrace` (line
509) both now read `!errors.As(err, &st) || isNilValue(st)` — the exact fix
0018 prescribed for M1 — while `isNilValue` itself (line 1109) is
byte-for-byte unchanged from 0018's L1 description. The v1.0.4 annotated tag
message confirms the release's own scope: "Closes the v1.0.3 assessment's M1
finding" (naming the finding, not paraphrasing it), and describes only the
stack-trace-guard and nil-logger changes — no mention of `isNilValue`'s
kind coverage. Source A's "premature" conclusion rests on treating its own
relabeled finding as the index's M1; once the labels are traced back to
their source (0018), the index claim is accurate and Source A's dispute
does not hold.

**Severity.** 0018 rated this exact gap Low (as L1) before v1.0.4 shipped.
Source B independently rates it Low. Source A rates it Medium without
engaging with 0018's own severity call or explaining what changed to
justify raising it. On the merits, independent of either source: the closed
M1 was reachable through unremarkable Go — a function typed to return a
concrete pointer error that is `nil` on a non-error path, one of the most
common accidental non-nil `error` values there is — and its consequence was
specific and severe (an already-written, correct response silently replaced
by a connection reset). The open L1 requires a caller to deliberately
implement `Coder` or `StackTracer` on an atypical concrete shape no built-in
`svcerr` type uses and few hand-written Go error types reach for. Its
consequence, tested fresh for this review (see Verification), is also
categorically milder: the panic occurs inside `GetErrorCode` at the top of
`writeJSONErrorBody`, before `finalizeErrorResponse` writes anything to the
`ResponseWriter`, so `RecoveryMiddleware` recovers before commitment and the
client still receives a 500 — not the client-visible `EOF` the closed M1
produced. Low is the correct rating; Source A's Medium is not adopted.

## Claim-by-claim verification

All claims reproduced fresh against `fe5a7af`, Go 1.26.5, darwin/arm64, via
a throwaway module (`replace github.com/n-ae/svcerr => <repo>`) built
outside the repository and removed after use.

| # | Claim (source) | Verdict | Evidence |
|---|---|---|---|
| 1 | `GetStackTrace` no longer panics on a typed-nil pointer-backed error (both sources; v1.0.4 tag) | **Confirmed fixed** | `var nf *svcerr.NotFoundError; var err error = nf; svcerr.GetStackTrace(err)` returns `[]`, no panic. `errors.go:1139-1145` reads `!errors.As(err, &st) \|\| isNilValue(st)`. |
| 2 | `RecaptureStackTrace` has the same fix (both sources; v1.0.4 tag) | **Confirmed fixed** | Same `err`: `svcerr.RecaptureStackTrace(err, 0)` completes, no panic. `errors.go:509-513` reads `!errors.As(err, &setter) \|\| isNilValue(setter)`. |
| 3 | `logError` returns before building log fields when `logger == nil` (both sources; v1.0.4 tag) | **Confirmed fixed** | `logging.go:79-85`: `if logger == nil { return }` precedes the `errorLogFields` call, with a comment explicitly citing this as avoiding an unnecessary `GetStackTrace` call. |
| 4 | `RecoveryMiddleware` no longer drops an already-good response for the pointer-backed typed-nil case (0018's original repro) | **Confirmed, regression test present and passing** | `TestRecoveryMiddlewareSurvivesATypedNilCoderThroughARealServer` (`http_test.go:1913`) passes in this environment: `go test -run TestRecoveryMiddlewareSurvivesATypedNilCoderThroughARealServer -v ./...` → `PASS`. Source A's environment could not bind loopback and left this unverified; this review's environment could and did. |
| 5 | `isNilValue` still checks only `reflect.Pointer` (both sources) | **Confirmed unchanged** | `errors.go:1109-1112`, byte-for-byte identical to the code 0018 quoted for L1. Doc comment (`errors.go:1099-1108`) is also unchanged, including the now-falsified claim that "no error type in practice uses" map/chan/func kinds. |
| 6 | A nil-valued non-pointer `Coder` still panics `GetErrorCode` (both sources) | **Confirmed** | `type nilSliceCoder []string` with `Code()`/`Error()` methods, assigned `nil`, boxed in `error`: `svcerr.GetErrorCode(err)` panics `index out of range [0] with length 0`. |
| 7 | The residual gap panics before response commitment, so `RecoveryMiddleware` recovers cleanly with a valid 500 rather than dropping an already-written response (neither source tested this directly) | **Confirmed, new evidence this cycle** | A handler calling `WriteHTTPError(w, nilSliceErr, nil)` behind `svcerr.RecoveryMiddleware(nil)`, served via `httptest.NewServer`: `http.Get` returns a normal response with `resp.StatusCode == 500`, not an `EOF`. `writeJSONErrorBody` (`render.go:239`) calls `GetErrorCode` as its first statement, before `finalizeErrorResponse` (`render.go:270`) touches the `ResponseWriter`. |
| 8 | v1.0.4's own stated scope matches 0018's M1, not 0018's L1 (this review) | **Confirmed** | Annotated tag `v1.0.4` message: "Closes the v1.0.3 assessment's M1 finding ... GetStackTrace and RecaptureStackTrace now share outermostCoded's isNilValue guard ... logError now returns immediately when the logger is nil." No mention of `isNilValue`'s kind coverage. |
| 9 | Two v1.0.2/v1.0.3-cycle design decisions (context-free `Logger`, marshal-panic stack provenance) remain unchanged and documented (carried over, not re-litigated this cycle) | **Confirmed, not new** | `docs/adr/0001-logger-has-no-context-parameter.md` and `docs/adr/0002-marshal-panic-log-keeps-the-original-errors-stack.md` both present; `logger.go`'s `Logger.Log` signature still takes no `context.Context`; `logging.go` still derives `stack_trace` from the original error. |

## Strengths

Unchanged from 0018 and re-confirmed rather than re-derived: single
finalization path for response writing with explicit header policy and
short-write detection; `Renderer` snapshotting configuration instead of
relying on package globals; a dependency-free root module with the zerolog
adapter correctly isolated as a separate, independently-versioned nested
module; CI covering both modules at their declared Go floor and stable Go,
including race detection and `govulncheck`. v1.0.4 adds to this list a
genuine, non-cosmetic fix applied consistently at all three of the choke
points 0018 named (`GetStackTrace`, `RecaptureStackTrace`, `logError`), with
regression coverage that exercises the fix through a real `net/http`
server rather than only a unit-level panic/recover — the harder and more
convincing form of test for a defect whose actual consequence was a
transport-level dropped connection, not just a panic message.

## Verification performed

At tag `v1.0.4` / HEAD `fe5a7af`, Go 1.26.5, darwin/arm64:

- `go test -count=1 -cover ./...` — 100.0% coverage, clean
- `go test -race -count=1 ./...` — clean
- `go test -run TestRecoveryMiddlewareSurvivesATypedNilCoderThroughARealServer -v ./...` — `PASS` (loopback bind succeeded in this environment, unlike Source A's sandboxed run)
- `go vet ./...`, `gofmt -l .` — clean
- `GOWORK=off go mod tidy -diff` — clean
- Nine fresh reproduction probes against a throwaway scratch module
  (`replace github.com/n-ae/svcerr => <repo>`), covering every claim in the
  table above, including one driven through a real `httptest.NewServer` to
  confirm the residual L1 gap panics pre-commitment rather than
  post-commitment — all deleted after use; `git status --short` shows only
  the pre-existing untracked Source A file
- Both sources' own verification (Source A: focused rendering/extraction/
  contract tests, `go vet`, `gofmt`, `go mod tidy -diff`, both modules,
  Go 1.26.5; Source B: `-cover`, `-race`, `-shuffle=on -count=20`,
  `GODEBUG=panicnil=1`, `go vet`, `gofmt`, `go mod tidy -diff`, Go 1.23.2)
  accepted as corroborating for what they cover, not substituted for the
  fresh reproductions above

## v1.0.5 priorities

1. ~~**L1 (carried over from 0018, unchanged priority: low)** — extend
   `isNilValue` (`errors.go:1109`) to switch over `Chan`, `Func`,
   `Interface`, `Map`, `Pointer`, `Slice` kinds, returning `IsNil()` for
   each; correct the doc comment's now-falsified claim that no error type
   in practice uses the non-pointer kinds. Add regression coverage for a
   nil-valued named slice or map `Coder`-only type through `GetErrorCode`,
   and the equivalent for `StackTracer` through `GetStackTrace`.~~ **Done
   — see [Addendum](#addendum-2026-07-19--l1-closed).**
2. **Documentation-only** — the assessment index (`docs/assessments/README.md`)
   should keep its "M1 closed in v1.0.4" wording as-is; it is correct.
   Any future shorthand referring to "the isNilValue gap" should cite it as
   L1, not M1, to avoid the mislabeling this cycle needed to untangle.
3. ADR 0001 and ADR 0002: no action, both re-confirmed present and
   correctly dispositioned, not re-opened.

The L1 fix remains non-breaking: it only narrows what a panic is allowed to
do, in a code path most consumers never exercise since svcerr's own types
and idiomatic custom errors are pointer-backed.

## Addendum (2026-07-19) — L1 closed

L1 is fixed at commit `1e8c547`, released as
[v1.0.5](https://github.com/n-ae/svcerr/releases/tag/v1.0.5); the adapter's
`svcerr` requirement was bumped accordingly in
[zerologadapter/v1.0.3](https://github.com/n-ae/svcerr/releases/tag/zerologadapter/v1.0.3).
`isNilValue` (`errors.go:1109`) now switches over `Chan`, `Func`,
`Interface`, `Map`, `Pointer`, and `Slice`, returning `IsNil()` for each
instead of checking only `reflect.Pointer`; the doc comment's claim that no
error type in practice uses the non-pointer kinds is removed.

Regression coverage added in `errors_test.go`:

- `TestGetErrorCodeWithNilNonPointerCoder` — a nil-valued `nilSliceCoder`
  (named `[]string` implementing `Coder`) classifies as `ErrCodeInternal`
  instead of panicking on `Code()`'s slice index.
- `TestGetStackTraceWithNilNonPointerCoder` — the `StackTracer` analogue,
  via `nilSliceStackTracer`.
- `TestGetErrorCodeWithNonNilCapableValueCoder` — a `structCoder` value
  (a kind reflect can never report as nil) still classifies normally,
  covering `isNilValue`'s `default` branch so a non-nil-capable value type
  isn't mistaken for nil.

Verified: `go test -count=1 -cover ./...` (100.0% coverage, unchanged),
`go test -race -count=1 ./...`, `go vet ./...`, `gofmt -l .`, and
`GOWORK=off go mod tidy -diff` — all clean.

## Follow-up (2026-07-19) — v1.0.6 adds further regression coverage

L1's fix itself did not move: it remains the `isNilValue` change released
in v1.0.5 above. [v1.0.6](https://github.com/n-ae/svcerr/releases/tag/v1.0.6)
is a separate, test-only release with no source or behavior change - it
adds dedicated nil map, chan, and func `Coder` regression tests
(`TestGetErrorCodeWithNilMapCoder`, `TestGetErrorCodeWithNilChanCoder`,
`TestGetErrorCodeWithNilFuncCoder`) alongside the slice case the v1.0.5
addendum above already covered, and documents `isNilValue`'s
`reflect.Interface` switch arm as defensive/unreachable through this
package's actual call sites rather than a coverage gap. The adapter's
`svcerr` requirement was bumped accordingly in
[zerologadapter/v1.0.4](https://github.com/n-ae/svcerr/releases/tag/zerologadapter/v1.0.4).

This closes 0019's only open finding. No new finding was introduced by the
fix. The "Note, not a finding" row above (the falsified doc comment) is
also resolved by this same commit. The rest of this document — including
the Source A/Source B reconciliation and the claim-by-claim verification —
describes the state of v1.0.4 at the time of the original review and is
left unchanged as a point-in-time record.
