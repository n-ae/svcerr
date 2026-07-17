# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v0.9.0 (`5418027`; adapter at `zerologadapter/v0.4.7`, one commit later, requirement bump only)
**Date:** 2026-07-17
**Reviewer:** maintainable-architect-v4
**Prior assessment (this series):** [docs/assessment-maintainable-architect-v4-v0.6.4.md](assessment-maintainable-architect-v4-v0.6.4.md) (v0.6.4, 2026-07-17)

---

## Disclosure

Everything between v0.6.4 and v0.9.0 was implemented in the same working
session as this review, by the same tooling, in response to the prior
assessment. A fresh review under that condition is worth doing but must
be read with that bias in mind. To compensate, verification here was run
against the published v0.9.0 tag in a detached worktree, with fresh
throwaway probes written to *falsify* the new code's claims rather than
re-run its own regression tests - and the probes did surface one new
finding (L1 below) in code added during that session.

---

## Verdict

v0.9.0 is the strongest release of this package to date and is
production-ready. Every actionable finding from both v0.6.4 assessments
has been closed across four releases (v0.6.5, v0.7.0, v0.8.0, v0.9.0),
each independently verified at its published tag during this cycle. I
found no critical, high, or medium defect in a fresh pass over the
current source.

| Severity | Count | Summary |
|---|---:|---|
| Critical / high / medium | 0 | — |
| Low | 3 | Reserved-name divergence between JSON and problem writers; `ExternalAPIError.RetryAfter` header gap; growing single-file/global-config surface |

The remaining items are polish and pre-v1 structural questions, not
correctness risks.

### What closed between v0.6.4 and v0.9.0

| v0.6.4 finding / recommendation | Closed in |
|---|---|
| M1: `RetryAfter` mutation bypassed the clamp | v0.6.5 — re-clamped at every emission point |
| M2: `PublicDetails` leaked internal maps | v0.6.5 — shallow copies, `Context()`-style contract |
| L1: `errors.Join` order-dependence undocumented | v0.6.5 — package doc, README section, pinning test |
| L2: commit-then-panic `WriteHeader` gap | v0.6.5 — validate 100-999, record before delegating |
| L3: log-field gaps (Conflict/RateLimit/Internal) | v0.6.5 — plus a completeness table test |
| L4: RFC 9457 reserved-member collision | v0.6.5 — reserved names dropped from extensions |
| L5: stale documentation pointers | v0.6.5 |
| CI race lane | v0.7.0 cycle — stable-toolchain `-race` for both modules |
| External contract suite + compiled examples | v0.7.0 — `package svcerr_test`, 10 output-checked examples |
| Default `WWW-Authenticate` mechanism | v0.7.0 — `SetDefaultAuthenticateChallenge`, error-specific wins |
| Configurable header policy | v0.8.0 — `HeaderPolicy` with independent normal/recovery slots |
| `WriteResult.BytesWritten` | v0.9.0 — plus `response_bytes_written` log parity |

Design quality of the additions, from this fresh read: the three
config mechanisms (`RegisterStatusCode`, `SetDefaultAuthenticateChallenge`,
`SetHeaderPolicy`/`SetRecoveryHeaderPolicy`) share one consistent idiom -
set once at startup, mutex-guarded, zero/empty value restores defaults -
and `HeaderPolicy`'s field polarities were chosen so its zero value is
bit-for-bit the pre-v0.8.0 behavior, which is exactly right for a
config struct retrofitted onto stable behavior. The normal-vs-recovery
policy split correctly models that the two paths write to different
points in the middleware stack. Probed safety edges all hold:
`WriteJSON(nil)` classifies as a 500 without panicking, and a custom
`Coder`-only code registered to 401 receives the default challenge.

---

## L1 (new): a reserved public-detail key silently diverges between writers

`SetPublicDetail` feeds both response shapes, but the two writers place
details differently: `WriteHTTPError`/`WriteJSON` nest them under
`error.details`, where no name is reserved, while `WriteHTTPProblem`/
`WriteProblem` flatten them to the top level, where v0.6.5's (correct)
RFC 9457 reservation now drops `type`, `title`, `status`, `detail`,
`instance`, and `code`. Reproduced at the tag:

```go
err := svcerr.NewNotFoundError("widget", "42")
err.SetPublicDetail("detail", "extra client hint")

svcerr.WriteJSON(w, err)    // details: {"detail":"extra client hint", ...}
svcerr.WriteProblem(w, err) // the key is silently absent
```

The same error renders different client-visible data depending on which
writer a handler picks, and the author of the `SetPublicDetail` call gets
no signal. The reservation itself is right - the fix is documentation
plus, optionally, an earlier signal:

1. `SetPublicDetail`'s doc comment (and the README's public-details
   section) should state that the six reserved names are omitted from
   problem-details output, naming `SetProblemType`/`SetProblemInstance`/
   `SetProblemTitle` as the intended spellings for three of them.
2. Optionally, a future release could log or surface reserved-key drops
   through the existing `WriteResult`/log-field channel. Renaming keys on
   the wire would be worse than dropping - don't.

## L2 (carried): `ExternalAPIError.RetryAfter` is details-only

An `ExternalAPIError` with `RetryAfter` set renders `retry_after` in the
JSON details but never a `Retry-After` header, while `RateLimitError`
sets both. Reproduced at the tag on a 502. RFC 9110 permits Retry-After
on any response and explicitly contemplates it with 503; a gateway
propagating an upstream's retry hint is a natural use. The asymmetry
predates this cycle (noted in the v0.6.4 assessment under M1's
periphery) and is now the only field whose documented contract is
post-construction assignment - the last concrete remnant of the
mutable-fields ambiguity that the pre-v1 immutability decision should
resolve. Either emit the header for it (behavior change, minor version)
or document why rate limits get a header and upstream retry hints don't.

## L3 (carried, grown): single-file and global-config accretion

`http.go` is now 1,396 lines (1,190 at v0.6.4); status mapping,
rendering, header policy, challenge policy, logging, response tracking,
and recovery still interleave in one production file, and the file
gained two more concern clusters this cycle. Alongside it, package-level
configuration now spans three independently mutex-guarded globals
(status registry, default challenge, two header-policy slots), each with
its own setter/getter idiom. None of this is wrong today - the idiom is
consistent and each global is small - but both curves point the same
direction. For v1: split `http.go` by responsibility (mapping,
rendering, policy, tracking, recovery), and decide whether a single
consolidated config (a `Renderer`/`Config` struct, as the external
review once sketched) should replace the accumulating globals. The
second decision interacts with the immutability question and belongs in
the same design pass.

Minor observability note, recorded here rather than as a finding: the
emission clamps added in v0.6.5 mean a mutated negative `RetryAfter` is
invisible in logs too (the log field shows the clamped `0` that was
sent). That is the documented, defensible choice - logs describe the
response - but a deployment debugging a misbehaving caller gets no
breadcrumb. Acceptable as-is; worth one sentence in the field's doc.

---

## Architecture and maintainability

What a fresh read finds working well:

- The root module remains dependency-free at a Go 1.20 floor, with the
  zerolog adapter isolated in its own tagged module; the requirement-bump
  release choreography has been followed consistently through v0.4.7.
- One classification node (`outermostCoded`) still drives status,
  message, details, headers, and log fields together, and every new
  feature this cycle (default challenge, header policy, byte accounting)
  was threaded through the shared body-writer helpers rather than
  duplicated per format - the drift risk the v0.6.4 assessment flagged
  in the three-renderer copy pattern did not materialize in this cycle's
  additions.
- The external contract suite means the exported API surface is now
  continuously exercised as consumers see it, and the ten output-checked
  examples pin the README's behavioral claims. The completeness table
  test closes the "high coverage doesn't prove projection completeness"
  gap for log fields.
- CI covers both modules at floor and stable, with lint, formatting, and
  a race lane. All observed runs this cycle were green on first attempt.

Test-architecture notes: tests and examples that touch global state
(`RegisterStatusCode`, the challenge default, header policies) isolate
themselves via cleanup/defer, verified under `-shuffle`; the compiled
examples necessarily leak two uniquely-named registry entries into the
test binary (no external cleanup API exists), which is acceptable but
worth remembering if a `ResetStatusCodes`-style test hook is ever added.
`WriteHeader`'s abort-vs-corrupt tradeoff remains documented at the site
and pinned by tests from both directions.

---

## Verification performed

Reviewed the published `v0.9.0` tag (`5418027`) in a detached worktree
using Go 1.26.5, independent of the working tree.

- `go build`, `go vet`, `gofmt -l` — clean, both modules
- `go test -count=1 -cover` — root 99.8%, adapter 100.0%
- `go test -race -count=1` — clean
- `go test -shuffle=on -count=3` — clean (global-state isolation holds)
- `GOTOOLCHAIN=go1.20.14 GOWORK=off go test` — root floor passes
- `GOWORK=off go mod tidy -diff` — no drift
- Fresh falsification probes (removed afterward): reserved-detail
  divergence (L1, new), `ExternalAPIError` header gap (L2), clamp
  observability (L3 note), nil-error and custom-401-with-default safety
  edges (both hold)

Earlier in this same cycle, each intermediate tag (v0.6.5, v0.7.0,
v0.8.0) was verified at publication against the module proxy and
checksum database, including MVS resolution through adapter-only
consumers and an end-to-end program against the published artifacts;
those results are recorded in assessment 0010 and the session's release
verifications, not re-run here.

---

## Revised rating

| Area | v0.6.4 | v0.9.0 |
|---|---|---|
| Problem selection | 9/10 | 9/10 |
| Scope and usability | 9.5/10 | 9.7/10 |
| Go idioms | 9.3/10 | 9.5/10 |
| Extensibility | 9.2/10 | 9.6/10 |
| Response safety | 9.6/10 | 9.7/10 |
| Middleware transparency | 9.4/10 | 9.7/10 |
| Production readiness | 9.5/10 | 9.8/10 |

The extensibility and transparency gains are earned by the config
mechanisms and the header-policy topology split; production readiness by
the contract suite, race lane, and the closure of every wire-correctness
finding. What keeps the ratings off 10 is structural, not behavioral:
the single-file hotspot, the accumulating globals, and the unresolved
mutability contract.

---

## Priority for v0.9.x and v1

1. Document the reserved-name divergence at `SetPublicDetail` and in the
   README (L1) - a doc-only patch.
2. Decide `ExternalAPIError.RetryAfter`'s header behavior (L2).
3. Before v1, in one design pass: immutable-vs-mutable semantic errors
   (the standing pre-v1 question), consolidation of the three global
   config surfaces, and the `http.go` responsibility split (L3). These
   three interact - a `Config`/`Renderer` object is also the natural home
   for per-instance (rather than global) policies, and immutable errors
   remove the need for emission-side clamping entirely.

v0.9.0 is a safe production choice for JSON, HTML, problem-details, and
streaming handlers, including behind custom writer stacks and
transparent compression when the header policy is configured to match
the deployment's middleware topology.
