# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v1.0.1 + post-tag HEAD (`8ef413c`; no production-code change since the tag)
**Date:** 2026-07-18 (implementation pass same day)
**Reviewer:** maintainable-architect-v4
**Status:** All v1.0.2 priorities implemented - see the closure note at the end
**Prior assessment (this series):** [docs/assessment-maintainable-architect-v4-v1.0.1.md](assessment-maintainable-architect-v4-v1.0.1.md)
**Cross-review inputs:** [docs/repository-assessment-head-codex-2026-07-18.md](repository-assessment-head-codex-2026-07-18.md) (Codex, direct, HEAD); an external review of v1.0.1 supplied by the maintainer (Go 1.23.2, root module only)

---

## Disclosure

Same-session bias continues, and this cycle it bit: one of the external
review's findings (F2 below) is a consequence this series' v1.0.1
assessment explicitly examined and waved off, and another (F1) sits in
the exact log path the v1.0.1 change set touched. Both are called out as
self-corrections below. Every cross-review claim was independently
reproduced with fresh probes (removed afterward, tree clean) before
being accepted — including a `GODEBUG=panicnil=1` run to reach the
legacy-semantics claim.

## Verdict

**v1.0.1 remains safe to adopt; a v1.0.2 is warranted and cheap.** The
external review is the most productive input this series has received:
five confirmed findings, none release-blocking, two of which correct
this series' own record. The Codex HEAD review independently confirms
the code posture (no code-level defect) and adds one operational
finding on the new vulnerability workflow. Everything actionable fits
in one small patch release plus one workflow commit.

| Severity | Count | Summary |
|---|---:|---|
| Critical / high | 0 | — |
| Medium | 1 | M1: committed/hijacked panic logs omit the panic stack |
| Low | 5 | L1 panicnil output-integrity edge; L2 marshaler-panic identity; L3 empty codes; L4 empty problem title; L5 workflow pinning/permissions |
| Deferred by design | 1 | Context-aware logger/renderer APIs — additive, demand-driven |

## Cross-review claim verification

All probes run at `8ef413c`, Go 1.26.5; the panicnil probe additionally
under `GODEBUG=panicnil=1`.

| Claim (source) | Verdict | Evidence |
|---|---|---|
| Committed-response panic logs omit the stack; `http_status=200` misleads (external F1) | **Confirmed** | `errorLogFields` gates `stack_trace` on `status >= 500` (`logging.go:24`); the committed branch passes `tw.status`. Probe: committed-200 panic → `stack_trace` absent, `http_status=200`; hijacked panic → absent; uncommitted panic → present. |
| `panic(nil)` marshaler under legacy panicnil emits truncated invalid JSON reported as success (external F2) | **Confirmed** | Probe under `GODEBUG=panicnil=1`: `WriteJSONResult` → `Status=400`, `RenderErr=nil`, body invalid, ends mid-details. Problem path self-corrected (encoding/json validates `MarshalJSON` output) — asymmetry confirmed too. |
| `RenderErr` from a panicking marshaler defeats `errors.Is`; panic-site stack lost (external F3) | **Confirmed** | `%v` at `render.go`'s `safeJSONMarshal`; probe: `errors.Is(RenderErr, sentinel) == false`. |
| Empty custom codes accepted end-to-end (external F4) | **Confirmed** | Probe: `New("", "oops")` renders `"code":""`, status 500; `GetErrorCode` returns `""`; registration APIs validate status range only. |
| Nonstandard status → empty problem `title` (external F5) | **Confirmed** | Probe: `"CUSTOM"→499` renders `"title":""` (`http.StatusText(499)` is empty). |
| govulncheck workflow floats `@latest`; neither workflow declares `permissions` (Codex L1) | **Confirmed** | `govulncheck.yml` installs `@latest` (this series wrote it); zero `permissions` blocks in either workflow file. |

The full suite also passes under `GODEBUG=panicnil=1` — which is
corroboration of the Codex observation, not a defense against F2: no
existing test attaches a nil-panicking marshaler in that mode.

## M1: committed and hijacked panic logs omit the panic stack

The single most valuable diagnostic for a panic — its stack — is
present in uncommitted-panic logs and absent in committed and hijacked
ones, because `errorLogFields` ties stack inclusion to an HTTP status
that, in those branches, describes the handler's *previous* response
(200) or nothing at all (hijack). The record is emitted at `LevelError`
either way; only the stack is lost. Mid-stream panics — SSE, streaming
JSON, downloads, WebSocket upgrades — are precisely the committed case.

**Self-correction:** the v1.0.1 change set edited this exact branch (it
fixed the status *fields*) and did not notice the stack gating one call
above. The external review found it while testing that very fix.

This is the series' first medium: no wire impact, but the recovery
middleware's committed branch exists almost entirely to produce a
diagnostic record, and it drops that record's most important field.

Fix direction (external review's, endorsed with one refinement): derive
panic-log fields from the recovered internal error's own severity —
`errorLogFields(err, http.StatusInternalServerError)` — then overwrite
the transport truth: drop `http_status` (no error response was
rendered), keep `response_committed_status` for the committed
non-hijack case, `hijacked=true` for hijack. The reviewer's
`logFieldOptions` struct is the cleaner long-term shape but is not
needed to close the finding; one integer parameter misused three ways
becomes two clearly-named fields with the simple fix. Note the
uncommitted branch keeps `http_status` — there a real error response
with that status *was* rendered.

## L1: `panic(nil)` output-integrity edge under legacy panicnil

Upgrade and self-correction of v1.0.1's N1, which said "nothing to do
beyond this record." That note evaluated the *panic path* (correct: it
degrades to stdlib behavior) but never chased the *output*: under
`GODEBUG=panicnil=1` or a pre-1.21 main module, the JSON envelope path
returns a truncated, invalid document with `RenderErr` nil and a full
`BytesWritten` — the API affirmatively claims success for a broken
body. That crosses from "documented GODEBUG nuance" into a violated
package invariant (WriteResult's contract is that render failure is
observable), even though every precondition is exotic.

The external review's fix is right and total: after a nil-error
`json.Marshal`, require `json.Valid(body)`; treat invalid output as a
render error and engage the standard fallback. This closes the legacy
mode, the modern mode, and any future encoder/adapter that returns
malformed bytes with a nil error, at the cost of one linear scan on the
error path only. Severity low (two opt-in preconditions), priority
high-within-low because the failure mode is a silent lie.

## L2: marshaler-panic `RenderErr` loses identity

`%v` flattens a panicked error value; `errors.Is` fails and the
panic-site stack is gone. Adopt the `%w` type-switch — it matches the
package's own wrapped-cause conventions. The proposed
stack-capturing private error type is declined: `RenderErr` already
names the mechanism, the wrapped value names the culprit, and a
marshaler panic is deterministically reproducible from its input;
capturing goroutine stacks at the recover site is machinery without a
2am story. Severity low.

## L3: empty codes are accepted end-to-end

`New("", ...)` produces a wire document whose machine-readable
identifier is the empty string. House style already answers this: the
package normalizes invalid values at entry points (the Retry-After
clamps) rather than at emission. Normalize `""` to `ErrCodeInternal` in
`New`/`Wrap` (the only custom-code entry points) and reject empty code
keys in `RegisterStatusCode`/`RendererConfig` validation, which already
return errors. `GetErrorCode` should stay honest about what an error
carries — with constructors normalized, a non-empty guarantee follows
for this package's own types. Severity low.

## L4: empty `title` for nonstandard statuses

`http.StatusText(499)` is empty and RFC 9457 wants a short
occurrence-invariant summary. Fall back to `defaultMessageForCode(code)`
when `StatusText` returns empty — occurrence-invariant by construction,
already the package's per-code generic text, no new configuration
surface. The richer `StatusMapping{Status, ProblemTitle}` config is
declined as speculative. Severity low.

## L5: vulnerability workflow reproducibility (Codex L1)

Accepted as written, both parts, both cheap: pin
`govulncheck@v1.6.0` (a floating scanner makes a future regression
unre-reviewable — this series wrote the workflow and should have caught
it against its own reproducibility standards) and add
`permissions: contents: read` to both workflow files so least privilege
is version-controlled rather than a mutable repo setting. SHA-pinning
actions is noted and deferred — without update automation it trades a
real maintenance cost for marginal gain in a two-workflow repo.

## Deferred: context-aware logging

The external review's `ContextLogger`/`JSONContext` sketch is a sound
additive v1.x design, and correctly identifies the limitation. Deferred
on the same standard as the constructor narrowing: additive API weight
is added on consumer demand, not speculation. It joins that item on the
demand-driven ledger.

## v1.0.2 priorities

1. **M1** — panic-log fields derived from the recovered error
   (stack always present), `http_status` dropped in committed/hijacked
   records, `response_committed_status`/`hijacked` as the transport
   truth; regression tests for all three branches' field sets.
   *Closed.*
2. **L1** — `json.Valid` guard in `safeJSONMarshal`; test via a
   stub marshaler returning invalid bytes with nil error (deterministic
   in every mode), plus the panicnil reproduction documented. *Closed*
   — the guard's own branch turned out to be reachable only through
   genuine legacy panicnil semantics (`encoding/json` validates any
   normally-returned `MarshalJSON` output through its own `compact()`
   call, so a non-panicking invalid-bytes marshaler is already rejected
   before reaching the new guard); pinned deterministically via a
   self-exec subprocess test running with `GODEBUG=panicnil=1`, keeping
   the package's default test run on standard semantics.
3. **L2** — `%w` for error-valued marshaler panics; `errors.Is` test.
   *Closed.*
4. **L3** — constructor normalization + registration rejection of
   empty codes. *Closed.*
5. **L4** — problem-title fallback for empty `StatusText`. *Closed*,
   including the same gap in the marshal-failure fallback body
   (`fallbackProblemBody`), found while implementing.
6. **L5** — workflow pin + permissions. *Closed*
   (`govulncheck@v1.6.0` pinned; `permissions: contents: read` added to
   both workflow files).
7. Context-logging deferral recorded alongside the constructor
   narrowing — unchanged, no action taken.

All non-breaking. Verified: `go test -count=1 -cover ./...` 100.0% both
modules; `-race`; `-shuffle=on -count=5`; `GODEBUG=panicnil=1 go test
-count=1 ./...`; `go vet`; `gofmt -l`; `golangci-lint run` 0 issues;
`GOWORK=off go mod tidy -diff` no drift; `govulncheck` no vulnerabilities
— all on both modules where applicable.

## Verification performed

At `8ef413c` (v1.0.1 + docs/index/workflow commits), Go 1.26.5,
darwin/arm64: root suite `-count=1 -cover` (100.0%), `-race`, vet,
gofmt — clean; full suite additionally under `GODEBUG=panicnil=1` —
clean; six fresh probes (removed afterward) confirming every
cross-review claim, one probe run in both panicnil modes. Both prior
reviews' own verification (Codex: both modules, both floors, lint,
tidy, CI links; external: root at Go 1.23.2 including `-shuffle
-count=20`) accepted as corroborating, not substituted for the above.

## Revised rating

| Area | v1.0.1 | HEAD (findings, pre-fix) | post-fix |
|---|---|---|---|
| Problem selection | 9/10 | 9/10 | 9/10 |
| Scope and usability | 9.8/10 | 9.8/10 | 9.8/10 |
| Go idioms | 9.8/10 | 9.8/10 | 9.8/10 |
| Extensibility | 9.7/10 | 9.7/10 | 9.7/10 |
| Response safety | 9.8/10 | 9.8/10 | 9.8/10 |
| Middleware transparency | 9.8/10 | 9.5/10 | 9.8/10 |
| Production readiness | 9.9/10 | 9.7/10 | 9.8/10 |

Middleware transparency takes the honest hit in the findings column: the
v1.0.1 mark was awarded partly for hijack-log honesty while a larger
dishonesty — the missing stack — sat one line above it. Production
readiness follows with the open medium. The finding source is worth
noting on its own: the series' event-driven posture worked exactly as
intended — external input arrived, and it found what the self-review
loop structurally could not.

The post-fix column is not a reflexive return to the prior marks.
Middleware transparency recovers to its v1.0.1 level because M1 is
closed with a structural fix (fields built from the recovered error's
own severity, not the transport status) — the same durable kind of
closure the v1.0.1 assessment credited for its own L1/L2. Production
readiness stops half a step short of its v1.0.1 mark: the two
same-session self-corrections this cycle required (L1's escalation from
"nothing to do" to a real fix, M1 sitting one line from code the
previous cycle had just edited) are themselves data about this review
loop's blind spot, not fully erased by closing what they found.
