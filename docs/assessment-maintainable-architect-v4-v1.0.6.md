---
title: Maintainable Architect Assessment — v4
version_reviewed: v1.0.6 (tag `v1.0.6` = commit `eff24fa`, the test-only source commit), zerologadapter v1.0.4; root module HEAD `0637ac7`
date: 2026-07-19
reviewer: maintainable-architect-v4
status: Accepted
---

# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v1.0.6 (tag `v1.0.6` resolves to commit `eff24fa`, "Add per-kind regression tests for isNilValue's map/chan/func arms" — the only source-affecting commit this cycle); HEAD `0637ac7` adds two follow-up commits on top (`2eb7084` bumps zerologadapter's `svcerr` requirement to v1.0.6, `0637ac7` notes this in the v1.0.4 assessment's addendum) — docs/dependency-metadata only, no further source change
**Date:** 2026-07-19
**Reviewer:** maintainable-architect-v4
**Prior assessment (this series):** [docs/assessment-maintainable-architect-v4-v1.0.4.md](assessment-maintainable-architect-v4-v1.0.4.md) (#0019 — reviewed v1.0.4, closed 0018's M1, carried forward L1 as open/low; later amended in place with two addenda documenting v1.0.5's L1 closure and a first-pass note that v1.0.6 is test-only)
**Cross-review input:** [docs/repository-assessment-v1.0.6-codex-2026-07-19.md](repository-assessment-v1.0.6-codex-2026-07-19.md) (Codex, direct review of HEAD `0637ac7`, zero findings, verdict: "release-ready," typed-nil gap confirmed closed)

---

## Disclosure

One cross-review input this cycle, not two — unlike 0017/0018/0019 there is no
second source to reconcile, and no disagreement to untangle. The Codex
review's "zero findings" verdict was not taken on trust: every claim below
was rebuilt independently — reading `errors.go` and `errors_test.go` directly
at HEAD, diffing against the `v1.0.5` tag, computing SHA-256 of `errors.go`
across the last three tags, running a throwaway scratch program (outside the
repository, deleted after use) to confirm a specific Go semantics claim in
the new tests' doc comments, and running the full verification suite fresh
in both modules. One discrepancy surfaced during this process that neither
the Codex file nor the v1.0.4 assessment's addendum flagged — see
[Note, not a finding](#release-notes-wording) below. `git status --short`
shows only the pre-existing untracked Codex file; nothing added by this
review.

## Verdict

**v1.0.6 is exactly what its own annotated tag says: a test-only release.**
`errors.go` is byte-for-byte identical (SHA-256) between v1.0.5 and v1.0.6;
the only file the two tags differ on that isn't docs or `go.mod` is
`errors_test.go`, which gains three new regression tests
(`TestGetErrorCodeWithNilMapCoder`, `TestGetErrorCodeWithNilChanCoder`,
`TestGetErrorCodeWithNilFuncCoder`) rounding out per-kind coverage of the
`isNilValue` fix v1.0.5 shipped. `isNilValue`'s runtime behavior — the fix
that closed 0018's L1 / 0019's carried-forward open finding — is unchanged.
This closes 0019's only open thread with no new finding introduced, matching
both the Codex review's conclusion and the v1.0.4 assessment's own
"Follow-up" addendum.

The one item worth recording is not a defect: the *published* GitHub release
notes for v1.0.6 say "no source or behavior change from v1.0.4/v1.0.5," which
is imprecise (v1.0.5 changed source relative to v1.0.4; only v1.0.6 itself
changed nothing relative to v1.0.5). Notably, the local annotated git tag
object carries different, already-precise wording ("no source or behavior
change from v1.0.5") — so the ambiguity exists only in the GitHub-side copy,
apparently hand-edited after tagging. This is cosmetic, doesn't affect any
consumer-facing behavior, contract, or version resolution, and is
dispositioned as a note rather than a finding, consistent with this series'
past treatment of similar documentation-only nits (e.g. 0019's own
"Documentation-only" v1.0.5 priority).

| Severity | Count | Summary |
|---|---:|---|
| Critical / high | 0 | No security, correctness, or response-corruption defect found |
| Medium | 0 | No reliability or API-contract defect found |
| Low | 0 | 0019's only open finding (L1, `isNilValue` kind coverage) was already closed in v1.0.5; v1.0.6 adds test coverage only, introducing nothing new |
| Note, not a finding | 1 | Published GitHub release notes for v1.0.6 said "no source or behavior change from v1.0.4/v1.0.5" — technically imprecise, since v1.0.5 changed source relative to v1.0.4. The annotated git tag itself was already worded precisely. **Resolved 2026-07-19 — see [Addendum](#addendum-2026-07-19--release-notes-wording-fixed) below.** The table row and body text that follow describe the point-in-time state when this assessment was first written. |
| Confirmed unchanged (carried over) | 2 | Context-free `Logger` (ADR 0001) and marshal-panic stack provenance (ADR 0002) — re-confirmed present, no action, not re-litigated this cycle. |

## Claim-by-claim verification

All claims reproduced fresh against HEAD `0637ac7` / tag `v1.0.6` (commit
`eff24fa`), Go 1.26.5, darwin/arm64.

| # | Claim (source) | Verdict | Evidence |
|---|---|---|---|
| 1 | v1.0.6's only source change is test additions; `errors.go` is unchanged from v1.0.5 (Codex; v1.0.6 tag) | **Confirmed** | `git diff v1.0.5..v1.0.6 --name-only` → `docs/assessment-maintainable-architect-v4-v1.0.4.md`, `docs/assessments/README.md`, `errors_test.go`, `zerologadapter/go.mod` only. `git diff v1.0.5..v1.0.6 -- errors.go` is empty. SHA-256 of `errors.go`: v1.0.5 = v1.0.6 = `4ebcac32d83052cb345be2fef6d4ef2b06d141f8c99f524764b41c53e8deda6c` (differs from v1.0.4's `7b976505...`, as expected, since v1.0.5 is where the fix landed). |
| 2 | `isNilValue`'s runtime behavior is unchanged from v1.0.5's fix (Codex) | **Confirmed** | `errors.go:1110-1118` reads a `switch rv.Kind() { case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice: return rv.IsNil() ... }` — identical to the v1.0.5 addendum's description in 0019, and covered by claim 1's byte-identity check. |
| 3 | Three new regression tests (nil map/chan/func `Coder`) exist and pass, each guarding a distinct `isNilValue` switch arm (Codex; v1.0.6 tag) | **Confirmed** | `errors_test.go:797-863` defines `nilMapCoder`/`nilChanCoder`/`nilFuncCoder` and three tests; each panics pre-fix (map write, chan close, nil func call) and asserts `ErrCodeInternal` post-fix. `go test -run 'TestGetErrorCodeWithNilMapCoder\|TestGetErrorCodeWithNilChanCoder\|TestGetErrorCodeWithNilFuncCoder' -v ./...` → all three `PASS`. |
| 4 | `reflect.Interface` remains in `isNilValue`'s switch but is unreachable through this package's actual call sites, since every call site converts an interface-typed local to `any` first, which Go flattens to the concrete type (v1.0.6 tag message, `errors_test.go` comment) | **Confirmed, independently reproduced** | All three call sites (`errors.go:511`, `1093`, `1147`) pass an interface-typed local (`setter`, `c`, `st`) to `isNilValue(v any)`. A throwaway scratch program confirms Go's semantics directly: assigning an interface-typed value holding a concrete struct to an `any` parameter reports `reflect.ValueOf(v).Kind() == struct`, not `Interface`; a nil interface value assigned the same way reports `Invalid`, not `Interface` — so the `Interface` arm is genuinely dead code through these call sites, not just asserted so. (Also moot in practice: `errors.As` already gates every call site, so a literal nil `error` never reaches `isNilValue` at all.) |
| 5 | zerologadapter's `svcerr` requirement is bumped to v1.0.6 (v1.0.6 tag; commit `2eb7084`) | **Confirmed** | `zerologadapter/go.mod`: `github.com/n-ae/svcerr v1.0.6`. |
| 6 | Codex's "zero findings, release-ready" verdict holds under a fresh, independent verification suite | **Confirmed** | See Verification performed, below — all checks clean in both modules, 100.0% coverage in both. |
| 7 | Published release notes wording is imprecise about which version(s) v1.0.6 has "no change" from (this review; not raised by the Codex file itself, which reports zero findings) | **Confirmed, but source of the claim differs from what was expected** | `gh release view v1.0.6 --repo n-ae/svcerr` returns: *"Test-only release: no source or behavior change from v1.0.4/v1.0.5."* The local annotated tag object (`git cat-file -p v1.0.6`) instead reads: *"Test-only release: no source or behavior change from v1.0.5."* — precise, and not ambiguous. The two differ, meaning the GitHub-side release description was edited independently of the tag after publishing. Note: `docs/repository-assessment-v1.0.6-codex-2026-07-19.md` as read for this cycle does **not** contain a release-notes-wording item — its Findings section is empty ("None"). The wording gap is real but was found by this review checking GitHub directly, not by re-deriving something already in the cited Codex file. |
| 8 | Two v1.0.2/v1.0.3-cycle design decisions (context-free `Logger`, marshal-panic stack provenance) remain unchanged (carried over, not re-litigated) | **Confirmed, not new** | `docs/adr/0001-logger-has-no-context-parameter.md` and `docs/adr/0002-marshal-panic-log-keeps-the-original-errors-stack.md` both present and unchanged; `logger.go`'s `Logger.Log` still takes no `context.Context`; `logging.go` still derives `stack_trace` from the original error. |

<a id="release-notes-wording"></a>
### Note, not a finding: release notes wording

Claim 7 above is worth a short standalone note because it didn't come from
where the task brief for this cycle expected it to: the local, untracked
Codex file (`docs/repository-assessment-v1.0.6-codex-2026-07-19.md`) reports
zero findings and does not mention release-note wording anywhere in its
text. The wording gap is nonetheless real — verified directly via
`gh release view v1.0.6`, which shows GitHub's copy of the release
description saying "from v1.0.4/v1.0.5" where the annotated git tag itself
says the more precise "from v1.0.5." Two independent facts, not one:

1. **The imprecision itself** is minor and harmless — a reader who checks
   v1.0.4 will find it did change (that's precisely 0019/v1.0.5's L1 fix),
   but the intended meaning ("v1.0.6 itself changed nothing beyond v1.0.5")
   is recoverable from context and from the commit history either way. It
   doesn't affect module resolution, `go.mod` requirements, or any
   consumer-visible behavior.
2. **The tag/release divergence** is a minor process observation: the
   GitHub release description for v1.0.6 was apparently hand-edited after
   the tag was pushed, since the two no longer match verbatim. Worth knowing
   for anyone treating the annotated tag as the single source of truth for
   release text (this series generally does, per the README index's note
   that "published GitHub release notes and annotated tag messages... link
   those paths directly") — in this one instance they've drifted.

Neither point rises to a finding. No commit, ADR, or corrective action is
proposed; if the release description is ever touched again, tightening the
wording to match the tag ("from v1.0.5") would be a one-line, zero-risk
edit, not something worth its own release.

## Strengths

Unchanged from 0019 and re-confirmed rather than re-derived: single
finalization path for response writing with explicit header policy and
short-write detection; `Renderer` snapshotting configuration instead of
package globals; a dependency-free root module with the zerolog adapter
correctly isolated as a separate, independently-versioned nested module; CI
covering both modules at their declared Go floor and stable Go, including
race detection and `govulncheck`. v1.0.6 adds one narrow thing to this list:
where v1.0.5 closed the `isNilValue` gap with tests for the slice arm and
the non-nil-capable default branch, v1.0.6 gives the map, chan, and func
arms their own named, independently-panicking regression tests instead of
leaving them as incidental coverage — a small but real improvement to how
loudly a future regression on any specific kind would fail, and it explains
in both code comment and tag message exactly why the sixth switch arm
(`reflect.Interface`) is deliberately untested rather than a coverage gap.
That kind of "why not" documentation, placed at the point of the decision
rather than left implicit, is exactly what this series has repeatedly asked
for elsewhere (see 0019's "Note, not a finding" about the previously
misleading doc comment) — v1.0.6 does it unprompted.

## Verification performed

At tag `v1.0.6` (commit `eff24fa`) / HEAD `0637ac7`, Go 1.26.5, darwin/arm64,
both modules:

- `go test -count=1 -cover ./...` — root 100.0%, adapter 100.0%, both clean
- `go test -race -count=1 ./...` — clean, both modules
- `go test -run 'TestGetErrorCodeWithNilMapCoder\|TestGetErrorCodeWithNilChanCoder\|TestGetErrorCodeWithNilFuncCoder' -v ./...` — all three `PASS` individually, not just as part of the full suite
- `go vet ./...` — clean, both modules
- `gofmt -l .` (each module) and `gofmt -l *.go zerologadapter/*.go` (combined, matching the Codex review's invocation) — clean
- `go build ./...` — clean, both modules
- `GOWORK=off go mod tidy -diff` — clean, both modules (the repo has a `go.work` at root listing `.` and `./zerologadapter`, so `GOWORK=off` is required in both directories to check each module's own `go.mod` in isolation, matching 0018/0019's invocation)
- `govulncheck ./...` — no vulnerabilities found (root)
- `git diff --check` — clean
- `git diff v1.0.5..v1.0.6 --stat` / `--name-only` and `-- errors.go` — confirms the only non-metadata source change is `errors_test.go`
- SHA-256 of `errors.go` at `v1.0.4`, `v1.0.5`, `v1.0.6` — v1.0.5 and v1.0.6 identical, v1.0.4 differs (expected)
- One throwaway scratch Go program (outside the repository, module `scratch`, deleted after use) confirming the `reflect.Interface`-is-unreachable claim in `errors_test.go`'s new doc comment against actual Go semantics, not just against the comment's own assertion
- `gh release view v1.0.6 --repo n-ae/svcerr` — confirms the published release description text and its divergence from the local annotated tag object (see [Note, not a finding](#release-notes-wording))
- The Codex review's own verification (full suite, both modules, Go 1.26.5, darwin/arm64, plus `git diff --check`) accepted as corroborating for what it covers, not substituted for the fresh checks above

## v1.0.7 priorities

None identified. 0019's only open finding (L1) is closed as of v1.0.5 and
remains closed; v1.0.6 added test coverage only. The release-notes wording
noted above is optional, zero-risk cleanup, not a priority. ADR 0001 and ADR
0002 remain correctly dispositioned, not re-opened.

<a id="addendum-2026-07-19--release-notes-wording-fixed"></a>
## Addendum (2026-07-19) — release notes wording fixed

The GitHub-side divergence noted in [claim 7](#release-notes-wording) is
resolved. The published v1.0.6 release description's opening line now reads:

> Test-only release: no source or behavior change from v1.0.5.

— identical to the annotated git tag object's wording, which never needed
correction. Verified via `gh release view v1.0.6 --repo n-ae/svcerr --json
body -q .body`, which now returns the corrected line; the rest of the release
body (regression-test rationale, the `reflect.Interface` unreachability
explanation, "No breaking changes," "Test coverage: 100.0%.") is unchanged
from the text quoted in claim 7's evidence.

This was optional, zero-risk cleanup by this assessment's own conclusion —
not something that warranted its own release or code change — and none was
made; only the GitHub release description text was edited. No finding was
open here to begin with, so nothing new closes; this addendum exists purely
to keep the note above from describing a state that no longer holds.
