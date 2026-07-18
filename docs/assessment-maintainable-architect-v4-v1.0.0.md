# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v1.0.0 (tag @ `aa133f0`) + zerologadapter/v1.0.0 (`main` @ `6abc6e4`, HEAD)
**Date:** 2026-07-17 (closure addendum 2026-07-18)
**Reviewer:** maintainable-architect-v4
**Status:** All findings closed in v1.0.1 - see the addendum at the end
**Prior assessment (this series):** [docs/assessment-maintainable-architect-v4-v1.0.0-rc.md](assessment-maintainable-architect-v4-v1.0.0-rc.md) (v1.0.0 candidate, 2026-07-17)
**Cross-review inputs:** [docs/direct-repository-assessment-v1.0.0-2026-07-17.md](direct-repository-assessment-v1.0.0-2026-07-17.md) (Codex, direct); an external review of the tagged release supplied by the maintainer (Go 1.23.2 environment)

---

## Disclosure

The bias disclosed in the rc assessment still applies: the code under
review was produced by the same tooling family as this review. Two
compensations this time. First, the tag now exists — this review ran
against the published `v1.0.0`/`zerologadapter/v1.0.0` history, not a
moving candidate. Second, two other reviews of this exact release were
available as input, and this assessment treated every one of their claims
as unverified until reproduced: four fresh falsification probes were
written against the tree (removed afterward, working tree left clean).
All four cross-review claims **confirmed**; none falsified. Where my
severity differs from a cross-review, the difference and the reasoning
are stated inline.

## Verdict

**v1.0.0 stands.** Nothing found by this review, the Codex direct
review, or the external review rises to a release defect: the wire
output in every reproduced case remains a safe, generic, valid response.
The findings are contract-consistency and diagnostics polish, all
addressable non-breakingly in v1.0.1. The rc assessment's "go" holds up
one release later — with one self-correction: the marshal-failure
fallback inconsistency (L1 below) existed in the candidate and this
series missed it; both cross-reviews found it independently.

| Severity | Count | Summary |
|---|---:|---|
| Critical / high / medium | 0 | — |
| Low | 3 | L1 fallback bypasses configured `INTERNAL_ERROR` status; L2 panicking custom marshaler escapes the fallback; L3 post-hijack panic diagnostics |
| Doc-only | 2 | D1 shallow-immutability wording; D2 README/logger drift |
| Carried from rc | 1 | Constructor narrowing (value-less `WrapValidationError`, query-less `NewDatabaseError`) — accepted, unchanged |

## Cross-review claim verification

Every claim was re-verified against source and, where behavioral, by a
throwaway probe run at HEAD with Go 1.26.5:

| Claim (source) | Verdict | Evidence |
|---|---|---|
| Marshal fallback hard-codes 500, ignoring a configured `ErrCodeInternal` status (both reviews) | **Confirmed** | `render.go:200`, `render.go:408`. Probe: renderer with internal→503 override; fallback wrote 500 for both `JSON` and `Problem` while the body said `INTERNAL_ERROR`; the same error without the unencodable detail wrote 503. Global `RegisterStatusCode` path identically affected. |
| Panicking custom `json.Marshaler` in a public detail propagates as a panic, not a `RenderErr` (external) | **Confirmed** | Probe: `MarshalJSON` that panics; `WriteJSONResult` panicked with the raw value. Matches `encoding/json`'s documented re-panic of non-sentinel panics. |
| Panic after successful `Hijack`: conn not closed by recovery, and the committed-branch log shows status 0 (external) | **Confirmed** | `commitOnHijack` (`tracking.go:145-151`) sets `wroteHeader` but never `status` and does not retain the conn. Probe: recovery re-panicked `http.ErrAbortHandler` (correct), logged `response_committed_status=0`, conn left open. |
| Identity immutability is shallow for `ValidationError` value (external) | **Confirmed** | `errors.go:521` stores the caller's reference. Probe: mutating the passed map after construction is visible through both `Value()` and `Context()`. |
| README links deleted `http.go`; two `interface{}` snippet spellings; stale nil-`err` example in `logger.go` (Codex L2) | **Confirmed** | `README.md:223`, `README.md:305`, `README.md:512`, `logger.go:17-18` (recovery always constructs an `InternalError`; nil-err reaches loggers via nil-error renders instead). |

## L1 (low): marshal-failure fallback bypasses the configured `INTERNAL_ERROR` status

Both `writeJSONErrorBody` and `writeProblemJSONBody` assign
`http.StatusInternalServerError` directly on a marshal failure instead
of resolving `s.status(ErrCodeInternal)` — so the one path that
*reclassifies* to `ErrCodeInternal` is the one path that ignores the
active mapping for that code. The body/status pair still disagrees with
the renderer's own contract (`RendererConfig.StatusCodes`: "a built-in
code may be overridden") and with `RegisterStatusCode`'s.

The external review rates this low-to-medium; I stay at **low**, with
Codex: it needs two opt-in edges simultaneously (a non-default internal
mapping *and* an unencodable public detail), and the wrong-status
response is still a valid, generic, non-leaking server error. What it
defeats is deliberate 503/retry semantics, which is real but narrow.

**Fix note both cross-reviews under-specify:** the one-line fix
(`statusCode = s.status(ErrCodeInternal)`) is necessary but not
sufficient, because `finalizeErrorResponse` still receives the
*original* error as `node`. Today the hard-coded 500 is load-bearing for
the WWW-Authenticate gate — the comment at `render.go:158` says so
("a fallback's 500 status already fails its 401 check"). Since renderer
and registry overrides accept any 400-599, a consumer may map
`ErrCodeInternal` to 401; after the naive fix, a fallback response would
then attach the original error's challenge (or the default) to a
response whose classification is no longer that error's. The fallback
should be modeled as a complete reclassification, as Codex recommends:
resolve status *and* classification-dependent headers from
`ErrCodeInternal`, gating the challenge on `fallback` exactly the way
Retry-After already is. Add composition tests for JSON and problem
output through both a `Renderer` override and `RegisterStatusCode`.

## L2 (low): a panicking custom `json.Marshaler` escapes the fallback

`SetPublicDetail` accepts arbitrary values; a `MarshalJSON` that panics
propagates out of `WriteJSONResult`/`Renderer.JSON` instead of becoming
the `RenderErr` + fallback body the marshal-*error* path provides.
Inside `RecoveryMiddleware` it is contained (at the cost of the real
response); on a bare writer it takes down the goroutine's error path.

The external review calls the recover-vs-document choice a policy
decision. I'll make the call this series should make: **recover it.**
This package already swallows third-party panics at exactly one other
boundary — `safeLog` (`logging.go:120`) unconditionally recovers logger
panics, on the reasoning that the diagnostic path must not be the thing
that fails. A caller-supplied marshaler is the same class of untrusted
diagnostic collaborator as a caller-supplied logger, and the package
already has the perfect degraded output for it (the fallback body,
`RenderErr` populated with a `svcerr: JSON marshaler panicked: %v`
error). Asymmetry here is harder to document than the recovery is to
write. Severity low: it takes a caller-authored panicking marshaler
placed in a public detail.

## L3 (low): post-hijack panic diagnostics

Not closing the hijacked conn on a recovered panic is **correct**, not a
gap — after `Hijack`, ownership is the handler's by net/http contract,
and handlers legitimately hand the conn to another goroutine before an
unrelated panic; auto-close would be a use-after-transfer hazard. Keep
the current behavior. Two small things are worth doing:

1. The committed-branch log after a hijack reports
   `response_committed_status=0` — a plausible-looking lie during a 2am
   incident. Record the hijack distinctly (a `hijacked=true` field, or
   set a sentinel status) so the log says what actually happened.
2. Document the `defer conn.Close()`-before-anything-that-can-panic
   pattern in the recovery section of the README, and pin the current
   behavior with a panic-after-hijack test (the probe written for this
   review is a ready template; the existing suite covers hijack
   commitment but not this log shape).

## D1 (doc-only): immutability wording is shallow at the reference boundary

Fields are unexported and unreassignable — the v1 invariant is real —
but a mutable object passed as a validation value stays shared, so
`Value()`/`Context()` observe later mutation. No wire or log impact
(validation values are deliberately never rendered or logged). The
public-details docs already state the equivalent shallow-copy caveat
(`errors.go:320`); the identity docs should say the same one sentence:
*identity references are fixed at construction; referenced objects are
not deep-copied.* No clone machinery — that's complexity the package
rightly refuses elsewhere.

## D2 (doc-only): three drifted references

`README.md:223` links `http.go`, deleted in the v0.11.0 split (now
`status.go`); `README.md:305` and `README.md:512` spell interface maps
as `map[string]interface{}` against the v1-wide `any` migration;
`logger.go:17-18` gives "the panic path" as the nil-`err` example, but
recovery has constructed a real `InternalError` for every recovered
panic — the honest example is a caller rendering a nil error. Three
one-line fixes.

## Carried: constructor narrowing (rc L1)

Unchanged from the rc assessment: `WrapValidationError` takes no value,
`NewDatabaseError` takes no query, both recorded in the tag message as
intended. Both cross-reviews concur with the disposition here: add
additive variants in v1.x only if a real consumer asks; never touch the
existing v1 signatures.

## v1.0.1 priorities

1. **L1** — fallback resolves status *and* headers from a full
   `ErrCodeInternal` reclassification via `s.status`; composition tests
   for renderer-override and registry-override, JSON and problem.
   *Closed in v1.0.1.*
2. **L2** — recover marshaler panics into `RenderErr` + fallback body
   (the `safeLog` precedent decides the policy question).
   *Closed in v1.0.1.*
3. **L3** — `hijacked` log field; recovery-section ownership note;
   panic-after-hijack regression test. *Closed in v1.0.1.*
4. **D1 + D2** — four one-line documentation corrections.
   *Closed in v1.0.1.*

All non-breaking; nothing here warrants expediting a release on its own.

## Verification performed

At `6abc6e4` (HEAD = zerologadapter/v1.0.0; root v1.0.0 = `aa133f0`,
whose only successor change is the adapter requirement bump), Go 1.26.5,
darwin/arm64:

- `go test -count=1 -cover ./...` — **root 100.0%, adapter 100.0%**
- `go test -race -count=1 ./...` — clean, both modules
- `go test -shuffle=on -count=5 ./...` — clean, both modules
- `go vet ./...` — clean, both modules
- `gofmt -l .` — no output
- Four falsification probes (`probe_assessment_v1_test.go`, removed
  after execution): fallback-status override (renderer + global, JSON +
  problem), panicking marshaler, post-construction value mutation,
  panic-after-hijack log shape and conn state — outcomes in the
  verification table above; working tree clean afterward.

## Revised rating

| Area | v1.0.0-rc | v1.0.0 |
|---|---|---|
| Problem selection | 9/10 | 9/10 |
| Scope and usability | 9.8/10 | 9.8/10 |
| Go idioms | 9.8/10 | 9.8/10 |
| Extensibility | 9.7/10 | 9.7/10 |
| Response safety | 9.8/10 | 9.7/10 |
| Middleware transparency | 9.7/10 | 9.7/10 |
| Production readiness | 9.9/10 | 9.8/10 |

The two ticks down are self-correction, not regression: the code is the
code the rc assessment approved; L1 and L2 were present then and
uncounted. A release whose worst post-tag findings are a
two-precondition status mismatch and a panic policy question is in
excellent shape — the v1.0.1 list above closes every open item this
series knows about.

---

## Closure addendum — v1.0.1 (2026-07-18)

All four priority items shipped in commit `f5667c6`, tagged `v1.0.1`
(annotated tag message is the changelog, per repository practice). CI
green on all four matrix lanes including the Go 1.21 Linux floor; both
modules held 100.0% statement coverage; `v1.0.1` verified resolvable
through proxy.golang.org.

Dispositions, including where implementation went beyond the list:

- **L1 closed.** The fallback is a complete reclassification: status
  from `s.status(ErrCodeInternal)` and the classification node dropped
  (`node = nil`), so `finalizeErrorResponse` lost its `fallback`
  parameter and no per-error header survives. The WWW-Authenticate
  nuance this assessment flagged is pinned by a dedicated test: with
  internal mapped to 401, a fallback carries the *default* challenge,
  never the original error's own.
- **L2 closed** in the recommended direction: `safeJSONMarshal` recovers
  caller-marshaler panics into `RenderErr` + the standard fallback,
  with the `safeLog` precedent cited in its doc comment.
- **L3 closed**, going one step further than recommended: the hijacked
  panic record gets its own message variant and `hijacked=true`, and
  drops `http_status` as well as `response_committed_status` (both
  zeros read as data). Connection ownership is documented in
  `commitOnHijack` and the README (`defer conn.Close()` pattern), and
  regression tests pin the log shape and that recovery leaves an
  unclosed conn alone.
- **D1 + D2 closed**, plus three same-class items found while closing:
  the by-reference caveat also added to the README identity paragraph,
  `Renderer.Middleware`'s "replacement 500" doc updated for the L1 fix,
  and `zerologadapter`'s `Log` signature migrated to
  `map[string]any` (type-identical; sits unreleased until the adapter
  next tags for a substantive reason).

The carried constructor narrowing (rc L1) remains open by design -
additive v1.x constructors only if real consumers surface the need.
Nothing else from this assessment is outstanding.
