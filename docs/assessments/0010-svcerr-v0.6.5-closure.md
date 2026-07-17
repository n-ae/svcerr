---
assessment: 0010
title: svcerr v0.6.5 closure of the maintainable-architect-v4 v0.6.4 assessment
date: 2026-07-17
status: Accepted
reviewer: maintainable-architect-v4
prior: ../assessment-maintainable-architect-v4-v0.6.4.md (v0.6.4)
---

# Assessment 0010: svcerr v0.6.5 closure of the maintainable-architect-v4 v0.6.4 assessment

## Scope

This is a closure review, not a fresh full audit: it verifies that v0.6.5
actually closes the seven v0.6.5 priority items defined by the
maintainable-architect-v4 v0.6.4 assessment
([docs/assessment-maintainable-architect-v4-v0.6.4.md](../assessment-maintainable-architect-v4-v0.6.4.md)),
against the real published tag rather than by re-reading commit messages.
It does not hunt for new issues beyond that assessment - a fresh full
review of v0.6.5 (if wanted) should be its own numbered assessment.

The seven items, and the findings they trace to:

1. Clamp `RetryAfter` at emission - header and details (M1).
2. Return copies from `BaseError.PublicDetails` (M2).
3. Document `errors.Join` classification order (L1).
4. Add the three missing log-field cases plus a completeness test (L3).
5. Reserve RFC 9457 member names when flattening extensions (L4).
6. Fix the three stale documentation pointers (L5).
7. Harden `WriteHeader` commitment tracking (L2).

## Verdict

v0.6.5 closes all seven items. `zerologadapter/v0.4.4` correctly picked up
the bumped `github.com/n-ae/svcerr v0.6.5` requirement per the documented
versioning policy, and both published modules resolve, verify against the
checksum database, compile, and exhibit the fixed behavior when consumed
through the public module proxy.

## Verification performed

**Tag-to-commit mapping.** `v0.6.5` (annotated) resolves to commit
`4b01bfb0c1c5a4c82c2c6d386eb54fdc030d4a80` - the single commit addressing
all seven items. `zerologadapter/v0.4.4` resolves to
`5b3922ccdef05e15bb9bafc7275307f2bdca84a3`, one commit past `v0.6.5`,
containing only the go.mod requirement bump. The proxy's origin metadata
for both versions reports exactly these hashes and refs.

**Independent re-reproduction at the tagged commit**, not the working
tree. Checked out `v0.6.5` into a detached worktree and, using fresh
throwaway probe tests rather than reusing the checked-in regression tests
(so the checked-in tests can't be the only thing "proving" the fixes):

- Item 1 (M1): constructed a `RateLimitError`, assigned `RetryAfter = -7`
  after construction, and asserted `Retry-After: 0` on the JSON,
  problem+json, and HTML renderings, `retry_after: 0` in the JSON details,
  and `retry_after: 0` in the structured log fields - all five emission
  surfaces agree. Confirmed fixed.
- Item 2 (M2): mutated the addition map returned by `PublicDetails` and
  asserted the injected key does not appear in the next rendered
  response. Confirmed fixed.
- Item 4 (L3): routed `ConflictError`, `RateLimitError`, and
  `InternalError` through `WriteHTTPError` with a capturing logger and
  asserted `resource_type`/`conflict_key`, `service`/`limit`/`retry_after`,
  and `component` respectively appear in the log fields. Confirmed fixed.
- Item 5 (L4): set public details named `instance` (an int) and `title`
  through `WriteProblem` and asserted the emitted object omits `instance`
  (no `SetProblemInstance` was called) and keeps the registered `title`
  ("Not Found"), with ordinary extension names still flattened. Confirmed
  fixed.
- Item 7 (L2): rebuilt the commit-then-panic scenario from scratch - an
  intermediate writer between `RecoveryMiddleware` and the recorder whose
  `WriteHeader` delegates (committing 204 downstream) and then panics
  once - and asserted recovery re-panics with `http.ErrAbortHandler`,
  leaving the committed 204 with an empty body instead of appending a
  second error document. Also asserted the guard the hardening must not
  regress: `WriteHeader(42)` through recovery still yields a real 500,
  even though the underlying recorder performs no status validation of
  its own (the tracker's own 100-999 validation panics pre-commitment).
  Both confirmed.
- Items 3 and 6 (L1, L5): confirmed by grep against the tagged tree - the
  README has the new "Joined errors" section and points to
  `HTTPStatusCode` for the status mapping, the package doc comment
  documents `errors.Join` traversal order with the explicit-`Wrap` idiom,
  and `defaultMessageForCode`'s comment now states it is *not* the RFC
  9457 title source.

**Full suite at the tagged commit** (isolated worktree, not the working
tree): `go build`, `go vet`, `go test -count=1`, `go test -race -count=1`,
and `gofmt -l .` all clean for both modules; coverage 99.8% (root), 100.0%
(zerologadapter). During the release itself the root module was also
built and tested green under the actual Go 1.20.14 floor toolchain
(`GOTOOLCHAIN=go1.20.14`), not just the current stable - notable because
item 2's fix deliberately avoids `maps.Clone` (Go 1.21) for that floor.

**Published-module consumption check** (the actual artifact a consumer
would `go get`, not the local git tree). In scratch modules outside this
repo, with a fresh empty `GOMODCACHE` to force real network fetches:

- `go get github.com/n-ae/svcerr@v0.6.5` and
  `go get github.com/n-ae/svcerr/zerologadapter@v0.4.4` both resolve
  through proxy.golang.org, record sum.golang.org-verified `go.sum`
  entries, and compile.
- A module requiring *only* the adapter (no direct `svcerr` requirement)
  correctly resolved `github.com/n-ae/svcerr` to `v0.6.5` via MVS,
  confirming the adapter's own bumped requirement drives resolution.
- A program built against both published modules, routing
  `svcerr.WriteHTTPError` through a `zerologadapter`-wrapped
  `zerolog.Logger` with a post-construction `RetryAfter = -9` mutation,
  produced the fixed behavior end to end: a 429 with `Retry-After: 0`,
  `retry_after: 0` in the JSON details, and a zerolog record carrying the
  new item-4 fields (`service`, `limit`, `retry_after`) with the same
  clamped value - items 1 and 4 verified through the published artifacts
  themselves, not just the source tree.

## Behavioral note carried forward

Item 7 is a deliberate tradeoff, not a pure fix, and the tagged source
documents it at the site: a delegated `WriteHeader` that panics on a
*valid* status without committing anything is now conservatively treated
as committed, so recovery aborts the connection instead of writing a
clean 500. The v0.6.4 assessment judged aborting strictly safer than
corrupting a response that may already be on the wire; nothing in this
closure changes that judgment. Deployments where recovery directly wraps
the server's own writer (the recommended layout) are unaffected either
way.

## Outstanding

None from the v0.6.4 assessment's items 1-7. Its v0.7.x recommendations
remain open by design: an application-wide default `WWW-Authenticate`
mechanism, a configurable header policy, an external-package contract/
example test suite, a CI race lane, and - before v1 - the
mutable-object-vs-immutable-value decision for semantic errors that
underlies M1/M2.
