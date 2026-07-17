---
assessment: 0005
title: svcerr v0.6.0 review
date: 2026-07-16
status: Accepted
reviewer: maintainable-architect-v4
prior: ../assessment-maintainable-architect-v4-v0.5.0.md (v0.5.0)
---

# Assessment 0005: svcerr v0.6.0 review

## Executive summary

v0.6.0 closes every finding from the four prior reviews; the full test and race suites pass on both modules, lint is clean, and coverage sits at 99.7%/100%. No critical or major findings remain — what's left is small-effort hardening: one obscure capability edge in the response-writer wrapper, one live copy-paste hazard in the constructors, and a handful of documentation and release-process gaps. The package is production-grade for its niche; the remaining work is about keeping it cheap to maintain, not making it safe to use.

Verification performed against this working tree (commit `55732cc`, tag v0.6.0): `go vet`, `golangci-lint` (0 issues), `go test -count=1` and `go test -race -count=1` on both modules, plus a focused reproduction for finding M1. No C4 diagram: this is a two-file library with no architecture change since the last assessment — a diagram here would be ceremony.

## Findings

### Critical

None.

### Major

None. The race suite — which the v0.5.0 external review could not complete in its environment — passes cleanly on both modules.

### Minor

**M1 — A `FlushError`-only writer gains a synthetic `http.Flusher` (S).**
`newTrackingResponseWriter` checks `flushErrorer` first and returns a variant implementing both `Flush()` and `FlushError()`. For an underlying writer that implements `FlushError() error` but *not* `Flush()` (legal — `http.ResponseController` documents them as alternatives), the wrapper therefore advertises `http.Flusher` where the underlying writer does not. Reproduced with a focused test: `underlying.(http.Flusher)` = false, `wrapped.(http.Flusher)` = true. This is the mirror image of the pre-v0.5.0 defect, but much less severe: the flush *capability* genuinely exists underneath (via `FlushError`), so the synthetic `Flush()` adapts a real capability rather than fabricating a missing one, and commit-tracking stays correct either way. Options: (a) document it as deliberate adapter behavior, or (b) split a `FlushError`-without-`Flusher` variant. (a) is proportionate.

**M2 — Duplicated reason-switch in the authentication constructors (S).**
`NewAuthenticationError` and `WrapAuthenticationError` each contain an identical four-case `reason` → code switch (errors.go:628-636 and 657-665). The same pattern was already extracted for the database constructors (`databaseErrorCode`) precisely because duplicated mapping logic drifts. Extract `authenticationErrorCode(reason string) ErrorCode`. More broadly, the 8 × `New*`/`Wrap*` pairs duplicate near-identical `BaseError` literals (~200 lines); an internal `newBase(code, message string, cause error, ctx map[string]interface{})` helper would halve that — but note `captureStackTrace(2)` is call-depth-sensitive, so a shared helper must adjust the skip. The switch extraction is the part that prevents real bugs; the broader helper is optional.

**M3 — Go floor higher than necessary; no Go-version CI matrix (S).**
`go.mod` declares `go 1.25.0` for a zero-dependency module whose newest stdlib usage is `http.NewResponseController` (Go 1.20, tests only). Two prior external reviews both ran the suite on Go 1.23 successfully (not independent corroboration - both came from the same review source, just at different points in the version history). A high floor costs adopters for nothing. Lower the directive to a verified minimum and add oldest+latest Go versions to the CI matrix so the floor is enforced rather than aspirational.

**M4 — zerologadapter release process is undefined (S).**
The adapter's last tag is `zerologadapter/v0.4.1` and its `go.mod` still requires `svcerr v0.4.0`. This *works* (the `Logger` interface hasn't changed since the split; consumers' MVS resolves the root module upward), but nothing documents whether the adapter is tagged per-release or only when it changes. Given the v0.4.0 ambiguous-import incident was exactly a nested-module release-process failure, write the policy down (README or a RELEASING note) — and bump the adapter's floor next time it's touched.

**M5 — Custom codes' message behavior is undocumented (S; carried from 0004/v0.5.0).**
The README's `RegisterStatusCode` section shows registering `ErrCodeOutOfStock` but never says its response message will be the generic "An unexpected error occurred." unless `SetPublicMessage` is called (custom codes aren't in `mayExposeOwnMessage`'s safe list). Secure by default, surprising in practice. One paragraph fixes it.

**M6 — Preserved-interface scope is implicit (S).**
The README says Flusher/Hijacker pass through "when (and only when)" supported, but never states the boundary: `http.Pusher` and `io.ReaderFrom` are dropped by the wrapper (disabling e.g. `sendfile` for wrapped handlers serving large files). Extending the variant set to cover them would double it — measure a real need first. Until then, one sentence documenting the scope.

**M7 — Assessment docs don't follow a convention (S; partially resolved by this assessment).**
Four prior assessments sit at `docs/` root with long unnumbered names and no index. This assessment starts `docs/assessments/` with `NNNN-` numbering and an index; the legacy files stay put because published GitHub release notes link their current paths. See consolidation plan.

## Over-engineered areas and missing capabilities

- **Vestigial: `ErrorWithCode`.** No function in the package requires it anymore — everything checks narrow capabilities. It's exported API, so keep it, but its doc comment could say plainly it exists for convenience/backward-compatibility only. Candidate for removal in a future v1 boundary.
- **At the edge, not over it: the six tracker variants.** A necessary consequence of Go's interface model; the alternative (generated 2^n wrappers à la httpsnoop) is worse for a solo maintainer. Documenting scope (M6) is the right containment.
- **The eight capability interfaces** are each one method and each demand-driven by a reviewed defect. Fine — but treat the pattern as complete; the next "SetX" should meet a high bar.
- **Missing (deliberate, acceptable):** per-request context in the `Logger` contract. The pure renderers (`WriteJSON`/`WriteHTML`/`WriteProblem`) are the designed escape hatch; no change recommended.

Accepted trade-offs, restated once (all documented in README/source; no action): `NewNotFoundError`'s message embeds the resource ID by default; built-in structured details are opt-out rather than opt-in; `Context()` is a shallow copy; errors are not safe for concurrent mutation.

## Recommendations

**This week (all S):**
1. Extract `authenticationErrorCode` to kill the duplicated switch (M2, narrow part). First step: move the switch into a function beside `databaseErrorCode`, call it from both constructors.
2. Document M1's adapter behavior, M5's custom-code message fallback, and M6's preserved-interface scope — three short README/doc-comment additions.

**This month:**
3. Lower the `go.mod` floor to a verified minimum and add a Go-version axis to the CI matrix (M3). First step: set `go 1.20` locally, run both suites, raise only if something breaks.
4. Write down the zerologadapter tagging policy (M4). First step: one paragraph in the README's logging section.

**This quarter (optional):**
5. Evaluate the internal `newBase` constructor helper (M2, broad part) — worthwhile only if constructors keep multiplying.
6. Revisit `io.ReaderFrom` passthrough only if a real workload shows the middleware on a large-file path (M6). Measure first.

## Consolidation plan

| File | Action | Reason |
|---|---|---|
| `docs/assessments/0005-svcerr-v0.6.0-review.md` | created (this file) | new convention: NNNN prefix, dedicated directory |
| `docs/assessments/README.md` | created | index covering 0005 + the four legacy assessments at their current paths |
| `docs/assessment-maintainable-architect-v4.md` (v0.3.0) | keep in place, index as 0001 | linked from published GitHub releases; moving breaks links |
| `docs/assessment-maintainable-architect-v4-v0.4.0.md` | keep in place, index as 0002 | same |
| `docs/assessment-maintainable-architect-v4-v0.4.1.md` | keep in place, index as 0003 | same |
| `docs/assessment-maintainable-architect-v4-v0.5.0.md` | keep in place, index as 0004 | same |

Future assessments go in `docs/assessments/` with the next number.
