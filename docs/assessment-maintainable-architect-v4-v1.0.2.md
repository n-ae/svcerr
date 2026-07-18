# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v1.0.2 (tag = HEAD, `6c3b86d`)
**Date:** 2026-07-18
**Reviewer:** maintainable-architect-v4
**Prior assessment (this series):** [docs/assessment-maintainable-architect-v4-head-2026-07-18.md](assessment-maintainable-architect-v4-head-2026-07-18.md) (v1.0.1 + HEAD cross-review; all six items closed in this tag)
**Cross-review inputs:** [docs/repository-assessment-v1.0.2-codex-2026-07-18.md](repository-assessment-v1.0.2-codex-2026-07-18.md) (Codex, direct, "Review A", zero findings); a second, independent Codex review of the same tag supplied by the maintainer in chat, not saved to disk ("Review B", five findings)

---

## Disclosure

The two inputs this cycle disagree completely — zero findings against five —
about the identical tagged commit. That is itself unusual enough to demand
the series' standard discipline twice over: nothing below is accepted from
either review's prose. Every one of Review B's five claims was reproduced (or
refuted) against actual v1.0.2 source with a fresh, throwaway Go program
(`reprotest`, built against this module via a `replace` directive, discarded
after use — no test files added to the repository, tree clean). Two claims
paged in a genuine, previously-uncaught defect; this is the series' second
straight cycle where an external, adversarially-minded review found what the
self-review loop and a non-adversarial third-party review both missed.

## Verdict

**v1.0.2 remains safe to adopt.** Review B is right about two real, narrow
defects — one worth a v1.0.3 patch, one worth a documentation line — and
wrong (or, more precisely, arguing a position this series already
considered and declined last cycle) about a third. Review A's "zero
findings" verdict is not fabricated: it correctly confirms every one of
v1.0.2's six release changes and re-runs the existing suite clean. It simply
never wrote an adversarial probe against a third-party `Coder`
implementation or the classic Go typed-nil-in-interface footgun — and no
existing test in the suite does either, so 100% statement coverage of the
*existing* tests could not have surfaced either gap.

| Severity | Count | Summary |
|---|---:|---|
| Critical / high | 0 | — |
| Medium | 1 | M1: a typed-nil coded error (e.g. `var *NotFoundError` in an `error`) panics response rendering |
| Low | 2 | L1: an external `Coder`'s empty `Code()` is not normalized at extraction; L2: the "(value, bool)" capability contract (`ProblemTitle`, `ProblemType`, `AuthenticateChallenge`) isn't documented as the implementer's responsibility |
| Note, not a finding | 1 | N1: a marshaler-panic log's `stack_trace` names the original error's construction site, not the marshal-panic site — accurate, not misleading, and declined for the same reason v1.0.2 declined capturing `RenderErr`'s own panic-site stack |
| Deferred by design | 1 | Context-aware `Logger`/`Renderer` APIs — carried over unchanged, additive on consumer demand |

## Reconciling Review A and Review B

Both reviews ran the same verification floor (both modules, `-race`,
`-cover` at 100.0%, `-shuffle`, `GODEBUG=panicnil=1`, `vet`, `gofmt`, `tidy
-diff`, `govulncheck`) and both confirm it clean here too. That floor answers
"does the existing test suite still pass" — it cannot answer "does an input
class no existing test constructs cause a defect," because coverage measures
lines executed by the tests that exist, not the space of inputs a public API
accepts. Review A's own scope section lists what it read (construction,
classification, rendering, headers, logging, recovery, renderer isolation)
but its verification section lists no new code written — only suite re-runs.
Review B's five claims are exactly the class of thing a coverage re-run
cannot find: a custom `Coder`-only type nothing in the suite constructs, and
a nil pointer stored in an `error` interface, which is `== nil` **false** and
sails past every `if err == nil` guard in the package (`render.go:551`,
`render.go:75-115`) while still being nil underneath. That is the same
lesson the HEAD cross-review drew from the external review one cycle ago:
this series' repeated blind spot is adversarial/third-party input, not
architecture or regression coverage — both of which the self-review loop and
Review A cover well.

## Claim-by-claim verification

All five claims reproduced fresh against `6c3b86d`, Go 1.26.5, darwin/arm64,
via a throwaway module (`replace github.com/n-ae/svcerr => <this repo>`),
removed after use.

| # | Review B claim | Verdict | Evidence |
|---|---|---|---|
| 1 | Empty `Coder.Code()` not normalized at extraction | **Confirmed** | `type emptyCoder struct{}` implementing only `Code() ErrorCode { return "" }` and `Error() string`, passed to `WriteJSONResult`: body is `{"error":{"code":"","message":"An unexpected error occurred."}}`, status 500. |
| 2 | Typed-nil coded error panics the renderer | **Confirmed** | `var appErr *svcerr.BaseError; var err error = appErr` (and separately `*NotFoundError`) into `WriteJSONResult`: `err == nil` is `false`, and the call panics with `runtime error: invalid memory address or nil pointer dereference`. |
| 3 | Marshaler-panic `RenderErr` lacks its own stack | **Confirmed as described, disputed as a defect** | `NewValidationError` + `SetPublicDetail` with a panicking `MarshalJSON`, rendered through `WriteHTTPError` with a capturing `Logger`: `stack_trace` is the `NewValidationError` call site; `response_render_error` carries the panic text with no accompanying stack. Accurate, not "misleading" — see N1. |
| 4 | No context-aware logging API | **Confirmed, not new** | `Logger.Log(level, err, fields, msg)` (`logger.go:22`) takes no `context.Context`. This is the exact item the HEAD cross-review already logged as "Deferred: context-aware logging" one cycle ago — Review B rediscovered an already-open, already-dispositioned item, not a new one. |
| 5 | Third-party capability return values fully trusted | **Partially confirmed** | Subsumed by #1 for `Coder.Code()`. For `ProblemTitle`/`ProblemType`/`AuthenticateChallenge`, `BaseError`'s own implementations already gate emptiness through the returned bool (`e.problemTitle != ""` at `errors.go:374`, same pattern at `:348` and `:391`) — a well-behaved third party following that convention is safe. Nothing stops a third party from returning `("", true)` in violation of the convention, but nothing in the package's own code is at fault; this is an undocumented contract, not a bug. |

### M1: a typed-nil coded error panics the renderer (claim 2)

Real, and the more serious of the two code-level findings. `errors.As`
matches a typed-nil `*BaseError` (or any BaseError-derived type — confirmed
separately for `*NotFoundError`) against `coderError` at `errors.go:1074`'s
`outermostCoded` without dereferencing anything, so the match succeeds
silently. `GetErrorCode` (`errors.go:1094`) then calls `node.Code()`, which
for `*BaseError` (`errors.go:226`) is `return e.code` — a struct field read
through a nil pointer, which is exactly where the panic fires, before
`writeJSONErrorBody` (`render.go:238-256`) reaches any other accessor. The
`if err == nil` guards already in the package (`render.go:551`, the top of
each `Write*` path) don't help: a typed nil wrapped in an `error` interface
is a non-nil interface value, so `err == nil` is `false` and every one of
those guards is bypassed exactly as designed by the language — this is not
a corner of `errors.As` behaving unexpectedly, it's the textbook Go
typed-nil-interface footgun, reachable by ordinary application code (a
function declared to return `*svcerr.NotFoundError` that returns a nil
pointer on some path, then gets assigned to a plain `error` variable before
reaching `WriteHTTPError`) — no adversarial or malicious type required.

Whether this actually crashes a service depends on whether the caller also
wraps the handler in `RecoveryMiddleware`/`Renderer.Middleware`: if so, the
panic is caught one frame up and reported as an internal error with a
`runtime error:` stack instead of the real classification — a real
information loss but not a process crash. Callers who invoke
`WriteJSONResult`/`WriteHTTPError` directly, without an enclosing recovery
layer (a legitimate, encouraged use of this package for a single-error path),
get an actual unrecovered panic in that request. Either way it defeats this
package's entire premise: it already treats a panicking third-party
`json.Marshaler` (`safeJSONMarshal`, `render.go:121-170`) and a panicking
`Logger` (`safeLog`, `logging.go:95-122`) as inputs that must never be
allowed to crash the very code whose job is reporting failures safely. A
typed-nil coded error is the same category of third-party input and is not
yet held to that standard — this is an inconsistency in the package's own
established defensive posture, not new scope invented by Review B.

**Worth fixing.** Not defensive-programming-for-hypothetical-misuse; this
is the single most common non-obvious way Go code accidentally produces a
non-nil error interface, and this package's job is specifically to accept
whatever `error` a caller hands it. Fix at the one choke point both
`GetErrorCode` and `writeJSONErrorBody`/`writeProblemJSONBody` already share
— `outermostCoded` — by checking `reflect.ValueOf(c).IsNil()` (guarded by
`Kind()` for the pointer/interface/map/chan/func/slice kinds `IsNil` accepts)
before returning a match, and treating a nil match as "no coded node found"
(the existing `nil` return path both callers already handle). One guard,
one call site, matches the package's existing pattern of centralizing a
cross-cutting concern in a single shared function rather than patching each
caller.

### L1: empty third-party `Coder.Code()` reaches the wire (claim 1)

Real, and a direct sibling of v1.0.2's own L3 fix. `normalizeCode`
(`errors.go:511`) is called only from `New` (`:525`) and `Wrap` (`:537`) —
the package's own construction entry points — and `RegisterStatusCode`
(`status.go:30-32`) separately rejects an empty *registration* key. Neither
guard reaches a bare third-party type that implements only `Coder` (the
package's own doc comment at `errors.go:7-10` explicitly advertises this as
a first-class, supported extension point, not an edge case): `GetErrorCode`
(`errors.go:1094`) returns `node.Code()` unmodified, so `""` rides straight
through to `ErrorDetail.Code` in the wire body. The v1.0.2 assessment's L3
disposition said "with constructors normalized, a non-empty guarantee
follows for this package's own types" — true as far as it goes, but it
leaves exactly the gap Review B found: the guarantee never extended to the
one extraction point every code path funnels through.

**Worth fixing, at low priority.** House style already answers where: this
package normalizes at the boundary closest to where an uncontrolled value
enters (the `RetryAfter` clamps, now `New`/`Wrap`). For a bare third-party
`Coder`, there is no construction-time hook to normalize at — `outermostCoded`
(the same choke point M1 already needs a nil guard in) is the boundary.
Fold a `normalizeCode` call into `GetErrorCode` immediately after extraction
(not into `outermostCoded` itself, which several call sites use for more
than the code) — one line, no new type, no new exported surface, closes the
same gap L3 already established was worth closing.

### L2: the (value, bool) capability contract is implicit (claim 5, narrowed)

Real but doc-only. `ProblemTitle`, `ProblemType`, and
`AuthenticateChallenge` all return `(string, bool)` where `BaseError`'s own
implementations tie the bool to non-emptiness (`errors.go:348`, `:374`,
`:391`). Nothing enforces that a third-party implementation follow the same
convention; one that returns `("", true)` produces an empty problem title
or challenge on the wire. This is a real gap in what's written down, not in
what the code does — the fix is a sentence on each interface's doc comment
(`Coder`, `ProblemTitler`, `ProblemTyper`, the `AuthenticateChallenge`
capability) stating that a `true` bool implies a non-empty string, matching
what `BaseError` already guarantees. No code change, no test — this is
exactly the kind of implicit decision the series' own conventions ask to be
made explicit, just in a doc comment rather than an ADR (the decision is too
small to warrant one).

### N1: marshal-panic stack names the wrong site — declined (claim 3)

Confirmed as described, and declined as a fix for the same reason the
v1.0.2 assessment declined a stack-capturing `RenderErr` type one cycle ago.
`errorLogFields` (`logging.go:17,25`) derives `stack_trace` from
`GetStackTrace(err)` — the *original* application error, captured at its own
construction site — regardless of whether a marshal panic occurred;
`logError` (`logging.go:79-93`) separately adds `response_render_error` with
the panic's text but no stack of its own. The field is not wrong: it
accurately answers "what error was being rendered," which is the more
useful fact for a 2am reader than the marshal panic's own site, which is a
custom `MarshalJSON` implementation — deterministic and fully visible by
reading that one function's source, not something a runtime stack adds
information to. Capturing a second PC stack at the `safeJSONMarshal` recover
site for this is the same "machinery without a 2am story" this series
already turned down for `RenderErr` itself. Documented as a note, not
fixed.

### Deferred, unchanged: context-aware logging (claim 4)

Already open. No new information from Review B beyond re-confirming
`Logger.Log`'s signature is unchanged — this item was logged as "Deferred:
context-aware logging" in the HEAD cross-review the cycle before this tag,
on the same additive-on-demand standard as the constructor narrowing. It
stays there.

## Verification performed

At `6c3b86d` (tag `v1.0.2` = HEAD), Go 1.26.5, darwin/arm64:

- `go test -count=1 -cover ./...` — root 100.0%, adapter 100.0%
- `go test -race -count=1 ./...` — clean
- `go vet ./...`, `gofmt -l .` — clean
- Five fresh adversarial probes against a throwaway module
  (`replace github.com/n-ae/svcerr => <repo>`), one per Review B claim,
  removed after use; repo tree left clean (`git status --short` shows only
  the pre-existing untouched Review A file)
- Both prior reviews' verification (Review A: full matrix including
  `-shuffle`, `GODEBUG=panicnil=1`, `tidy -diff`, `govulncheck`, both
  modules; this series' own HEAD cross-review closing all six prior items)
  accepted as corroborating for what they cover, not substituted for the
  adversarial probes above, which neither prior review ran

## Revised rating

| Area | v1.0.1 | HEAD post-fix | v1.0.2 (this review) |
|---|---|---|---|
| Problem selection | 9/10 | 9/10 | 9/10 |
| Scope and usability | 9.8/10 | 9.8/10 | 9.8/10 |
| Go idioms | 9.8/10 | 9.8/10 | 9.6/10 |
| Extensibility | 9.7/10 | 9.7/10 | 9.6/10 |
| Response safety | 9.8/10 | 9.8/10 | 9.5/10 |
| Middleware transparency | 9.8/10 | 9.8/10 | 9.8/10 |
| Production readiness | 9.9/10 | 9.8/10 | 9.6/10 |

Response safety takes this cycle's real hit: the package's central promise —
that reporting an error is itself panic-safe against whatever a caller or
third-party collaborator hands it — already held for panicking marshalers
and panicking loggers, and does not yet hold for a typed-nil coded error,
which is a more common accident than either of those. Go idioms follows for
the same reason from a different angle: accepting `error` at every public
entry point while remaining vulnerable to the most well-known Go interface
footgun is an idiom gap, not a style one. Extensibility ticks down a notch
because the same capability-sized-interface design this series has praised
every cycle (`Coder`-only types, no `BaseError` embedding required) is
exactly the surface both M1 and L1 enter through — the design remains
right, but its edges aren't yet as hardened as the package's own core
constructors. Production readiness follows the weighted average of a real,
if narrow-trigger, crash bug found after a tag this series and Review A both
initially cleared. None of the three drops reflect the deferred
context-logging item or the declined stack-provenance note — those are
carried at their existing weight, not re-penalized.

## v1.0.3 priorities

1. **M1** — nil-guard `outermostCoded` (`errors.go:1074`) with a
   `reflect`-based `IsNil` check across the kinds it applies to, treating a
   typed-nil coded error as "no coded node" (the same `nil` both
   `GetErrorCode` and the body writers already handle); regression tests
   for a typed-nil `*BaseError` and at least one semantic type (e.g.
   `*NotFoundError`) through `WriteJSONResult`/`WriteProblemResult` and
   through `RecoveryMiddleware`-wrapped and unwrapped call paths.
2. **L1** — normalize the code `GetErrorCode` returns (not just what
   `New`/`Wrap` accept) so an external bare-`Coder` type's empty string is
   caught at the one extraction point every renderer funnels through;
   regression test with a `Coder`-only custom type returning `""`.
3. **L2** — one doc-comment sentence per capability interface
   (`Coder`, `ProblemTitler`, `ProblemTyper`, the `AuthenticateChallenge`
   method) stating the `(value, bool)` convention: `true` implies
   non-empty. No code change.
4. N1 (marshal-panic stack provenance) and the context-aware logging
   deferral: no action, both already dispositioned above and in the prior
   cycle respectively.

All four items are non-breaking, additive-at-most changes with no wire or
API-shape impact except the (already-intended) narrowing of what `""` or a
nil pointer is allowed to do — a caller relying on the current panic or the
current empty wire code to happen is not a compatibility concern worth
preserving.
