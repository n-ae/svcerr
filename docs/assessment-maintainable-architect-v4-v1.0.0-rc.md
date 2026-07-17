# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v1.0.0 candidate (`main` @ `27324a5`, untagged)
**Date:** 2026-07-17
**Reviewer:** maintainable-architect-v4
**Prior assessment (this series):** [docs/assessment-maintainable-architect-v4-v0.9.0.md](assessment-maintainable-architect-v4-v0.9.0.md) (v0.9.0, 2026-07-17)
**Design input:** [docs/v1-design-pass.md](v1-design-pass.md) (with its two dated amendments)

---

## Disclosure

Everything under review - the entire pre-v1 design pass, stages 0 through
3, spanning v0.11.0 through this candidate - was designed and implemented
in the same working session as this review, by the same tooling. That is
a stronger bias than the v0.9.0 assessment carried, and the compensation
is the same but firmer: verification against the clean candidate tree
(`git status` empty at `27324a5`; no tag exists yet to check out
independently), with fresh falsification probes aimed specifically at
what an executor is most likely to miss - and one of those probes did
surface this review's only new finding (L1 below).

## Verdict

**Go for v1.0.0.** The candidate delivers the design pass exactly as
amended, closes the mutability root cause that every assessment since
v0.6.4 traced findings to, and does so with the strongest verification
posture the project has had: **100.0% statement coverage on both
modules** (up from 99.8%/100%), race- and shuffle-clean, CI green on the
new Go 1.21 floor (Linux floor lane; locally the 1.21 toolchain compiles
and vets but its darwin test binary trips a known dyld/LC_UUID issue
unrelated to this code). I found no critical, high, or medium defect.

| Severity | Count | Summary |
|---|---:|---|
| Critical / high / medium | 0 | — |
| Low | 1 | Constructor coverage gap for two previously-assignable optional identity values |

### What this candidate completes

| Design-pass item | Status |
|---|---|
| `http.go` split + centralized finalization (stage 0) | Shipped, v0.11.0 |
| `Renderer` with logger on config (stage 1) | Shipped, v0.12.0 |
| `SetRetryAfter` + read-only contract (stage 2, as amended) | Shipped, v0.13.0 |
| 18 identity fields unexported, bare same-name accessors | In candidate |
| `AuthenticationError.SessionID` removed (inert write-only field) | In candidate |
| `Context()` derived per type, snapshot map deleted | In candidate |
| Emission clamps removed; invariant at the two entry points | In candidate |
| Go floor 1.21; `maps.Clone`; `interface{}` → `any` | In candidate |
| Soft deprecation of the `WriteHTTP*`/`*Result` triples | In candidate |

## Probes and what held

Fresh throwaway probes (removed afterward), each written to falsify a
claim of the new code:

- **No `*BaseError` backdoor.** `errors.As` with a `*BaseError` target
  does not match a semantic error (the embedded `BaseError` is not in
  the `Unwrap` chain), so no caller can reach the nil base `Context()`
  for an error whose concrete type derives a real one. Held.
- **Concurrent accessor reads are race-clean.** The new documentation
  claims identity accessors and `Context()` are safe to call
  concurrently once construction/configuration is done; 8 goroutines ×
  500 iterations over accessors, `Context()`, `Error()`, and
  `GetErrorCode` under `-race` confirm it. Held.
- **No stale documentation.** Greps for the pre-v1 phrasings
  ("read-only identity fields", shallow-copy `Context` caveats, `1.20`
  floor references) and for `interface{}` remnants across source and
  README all come back empty. Held.
- **Constructor coverage** - this one did not hold cleanly; see L1.

The existing suite carries the structural proofs: the retargeted M1 test
feeds the constructor `-9` and asserts all five emission surfaces agree
(proving the invariant that replaced the removed clamps),
`TestContextDerivation` pins every type's derived map including the three
documented normalizations plus per-call freshness, and
`TestRendererZeroConfigMatchesPackageDefault` still pins byte-parity
between instance and package-level rendering.

## L1 (new): two previously-assignable optional identity values lost their only entry point

Before v1, a consumer could attach optional identity after construction
through the exported fields. Two of those values have no v1 replacement:

- `WrapValidationError` takes no `value` - `Value()` is always nil on
  the wrap path (only `NewValidationError` captures one);
- `NewDatabaseError` takes no `query` - `Query()` is always empty on the
  New path (only `WrapDatabaseError` captures one).

Reproduced by probe. The other two losses were deliberate, recorded
decisions (`SessionID` removed as inert; the retry hint got
`SetRetryAfter`), but these two are silent narrowings the design pass
never called out.

Severity is low with reasoning: neither value is ever rendered to
clients (`extractErrorDetails` deliberately excludes both) nor logged
(`errorLogFields` excludes both as sensitive); they surface only through
the accessors and `Context()`, both consumer-facing conveniences. The
constructors have never captured these combinations, so the loss affects
only consumers who used the field assignability the whole release is
designed to remove.

Recommendation: **accept for v1.0.0** - the identity-from-constructors
model is the point, and adding parameters now would churn the exact
signatures v1 stabilizes. Record the narrowing in the v1.0.0 tag
message's migration notes. If real consumers surface with the need,
additive v1.x options (`WrapValidationErrorValue(...)` variants or a
functional option) cover it without breaking anything.

## Notes, not findings

- **`RetryAfter()` asymmetry** - `RateLimitError.RetryAfter() int` vs
  `ExternalAPIError.RetryAfter() (int, bool)` - is correct, not
  accidental: the first is always set by construction, the second is an
  optional hint. Both doc comments say so.
- **No standalone migration guide file.** The README documents the
  accessor model and the package doc carries the `x.Field` → `x.Field()`
  note; the plan puts the full migration guide in the v1.0.0 tag
  message. That is consistent with this repository's release practice
  (annotated tags as changelogs), but the tag message must then actually
  enumerate: the parens migration, the `RetryAfter()` two-value shape,
  `SessionID` removal, the three `Context()` normalizations, the L1
  narrowing, and the floor raise.
- **The soft-deprecation choice** (guidance text, not `// Deprecated:`
  markers) is right for functions that remain fully supported - formal
  markers would fire SA1019 across every consumer - but it means no
  tooling nudge exists. If v2 ever plans removal, markers should precede
  it by a full minor release.

## Architecture and maintainability

The end state is what the assessments asked for across four releases:
one canonical identity per error with every projection (details,
headers, log fields, `Context()`) derived from it; validity invariants
enforced at entry points instead of re-established at emission sites;
configuration available per-instance with the globals as a convenience
layer; and the HTTP machinery split into six single-responsibility files
with the shared delivery sequence in one place. The remaining structural
observations from v0.9.0 (single-file hotspot, global accretion,
mutability ambiguity) are all resolved; nothing comparable replaces
them. Module inventory at the candidate: 115 exported functions/types
across ~8,300 lines including tests, both modules at 100.0% coverage,
root still dependency-free at a Go 1.21 floor.

## Verification performed

At the clean candidate tree (`27324a5`), Go 1.26.5 unless stated:

- `gofmt -l`, `go vet` — clean, both modules
- `go test -count=1 -cover` — **root 100.0%, adapter 100.0%**
- `go test -race -count=1` — clean (including the concurrency probe)
- `go test -shuffle=on -count=3` — clean
- `GOTOOLCHAIN=go1.21.13 go build && go vet` — floor compiles and vets
  locally; the floor *test run* is proven by CI's Linux floor lane
  (green on this commit), since the darwin go1.21 test binary trips a
  known dyld/LC_UUID incompatibility unrelated to this code
- `GOWORK=off go mod tidy -diff` — no drift
- Four falsification probes (removed afterward), one yielding L1

## Revised rating

| Area | v0.9.0 | v1.0.0-rc |
|---|---|---|
| Problem selection | 9/10 | 9/10 |
| Scope and usability | 9.7/10 | 9.8/10 |
| Go idioms | 9.5/10 | 9.8/10 |
| Extensibility | 9.6/10 | 9.7/10 |
| Response safety | 9.7/10 | 9.8/10 |
| Middleware transparency | 9.7/10 | 9.7/10 |
| Production readiness | 9.8/10 | 9.9/10 |

## Release checklist for v1.0.0

1. Tag with the full migration guide in the annotated message (see the
   enumeration under "Notes"). Module path stays `github.com/n-ae/svcerr`.
2. After the tag publishes: bump the adapter requirement to v1.0.0 and
   tag the next `zerologadapter` release; verify proxy/checksum-db
   resolution and adapter-only MVS as in prior cycles.
3. Post-release: keep L1 on the v1.x radar; consider `// Deprecated:`
   markers on the six soft-deprecated functions one minor release before
   any v2 removal decision.

No blocker stands between this candidate and the tag.
