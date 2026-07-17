---
assessment: 0009
title: svcerr v0.6.4 closure of assessment 0008
date: 2026-07-17
status: Accepted
reviewer: maintainable-architect-v4
prior: 0008-svcerr-v0.6.3-review.md (v0.6.3)
---

# Assessment 0009: svcerr v0.6.4 closure of assessment 0008

## Scope

This is a closure review, not a fresh full audit: it exists to verify that
v0.6.4 actually closes assessment 0008's L1-L6 and its short-write
hardening item, against the real published tag rather than by re-reading
commit messages. It does not hunt for new issues beyond what 0008 already
covers - a full fresh review of v0.6.4 (if wanted) should be its own
assessment.

## Verdict

v0.6.4 closes all of assessment 0008: L1 (`Unwrap`-chain commit-tracking
bypass), L2 (log-metadata/response-classification mismatch), L3 (invalid
`WriteHeader` status falsely recorded as committed), L4 (HTML `Retry-After`
gap), L5/L6 (README and `WriteResult.Status` doc drift), and the
short-write hardening suggestion. `zerologadapter/v0.4.3` correctly picked
up the bumped `github.com/n-ae/svcerr v0.6.4` requirement per the
documented versioning policy.

## Verification performed

**Tag-to-commit mapping.** `v0.6.4` (annotated) resolves to commit
`59719b8724be86428fb7498de266fbcc51326ac6` - the short-write hardening
commit, the last of the four commits addressing 0008
(`d50ff55` L1/L2, `feb73ee` L3/L4, `2112039` L5/L6, `59719b8` hardening).
`zerologadapter/v0.4.3` resolves to `98acb24a4be4e31f879a54b01159393f289f6431`,
one commit past `v0.6.4`, containing only the go.mod requirement bump.

**Independent re-reproduction at the tagged commit**, not the working
tree. Checked out `v0.6.4` into a detached worktree and, using fresh
throwaway test files rather than reusing the checked-in regression tests
(so the checked-in tests can't be the only thing "proving" the fix):

- L1: rebuilt the `Unwrap`-only wrapper scenario from scratch
  (`http.NewResponseController(w).Flush()` through a wrapper implementing
  only `Unwrap() http.ResponseWriter`, then a panic) and asserted
  `RecoveryMiddleware` aborts with `http.ErrAbortHandler` instead of
  returning normally, with the recorder showing a flushed-but-unmodified
  body (no second response appended). Confirmed fixed.
- L2: called `errorLogFields` directly on a `WrapNotFoundError`-wrapped
  `DatabaseError` and asserted the logged fields carry the outer
  `NotFoundError`'s `resource_type`/`resource_id`, not the inner
  `DatabaseError`'s `db_operation`. Confirmed fixed.
- L3/L4/hardening: re-ran the existing checked-in regression tests
  (`TestRecoveryMiddlewareWritesARealErrorResponseAfterAnInvalidStatusPanic`,
  `TestWriteHTTPErrorHTMLSetsRetryAfterHeader`,
  `TestWriteResultFunctionsMirrorTheirIntCounterparts`) directly against
  the tagged commit - all pass.
- L5/L6: confirmed by grep against the tagged tree - README's
  `RecoveryMiddleware` section describes the `http.ErrAbortHandler`
  re-panic (not "just logs"), and `WriteResult.Status`'s doc comment says
  "the status code svcerr selected and passed to `WriteHeader`" (not
  "actually sent").

**Full suite at the tagged commit** (isolated worktree, not the working
tree): `go build`, `go vet`, `go test -count=1`, `go test -race -count=1`,
`gofmt -l .`, and `golangci-lint run` all clean; coverage 99.8%.

**Published-module consumption check** (separate from the source-level
verification above - this checks the actual artifact a consumer would
`go get`, not the local git tree). In scratch modules outside this repo:

- `go get github.com/n-ae/svcerr@v0.6.4` resolves through the module
  proxy; a program built against it using `WriteJSONResult` on a
  `NotFoundError` produced the correct 404 JSON.
- `go get github.com/n-ae/svcerr/zerologadapter@v0.4.3` (note: the nested
  module's tag prefix is stripped for `go get` - `@v0.4.3`, not
  `@zerologadapter/v0.4.3`) resolves through the proxy. A module requiring
  *only* the adapter (no direct `svcerr` requirement) correctly resolved
  `github.com/n-ae/svcerr` to `v0.6.4` via MVS, confirming the adapter's
  own bumped requirement is what drives resolution, not an incidental
  higher requirement from elsewhere. A program built against both,
  routing `svcerr.WriteHTTPError` through a `zerologadapter`-wrapped
  `zerolog.Logger`, produced the correct 500 JSON.
- Incidental, expected, not a defect: `go install github.com/n-ae/svcerr@v0.6.4`
  fails with `package github.com/n-ae/svcerr is not a main package` -
  correct, since svcerr is a library with no `main` package. `go get`/
  import is the applicable operation, which works.

**Workspace check.** `go build`/`go vet`/`go test` `./... ./zerologadapter/...`
from the repo root (plain `./...` alone only covers the root module - a
nested `go.mod` is a module boundary `./...` doesn't cross even under
`go.work`) all clean on the current working tree (`98acb24`, one commit
past `v0.6.4`). `go work sync` produced no drift.

## Outstanding

None from assessment 0008. If a fresh full review of v0.6.4 is wanted
(new issues beyond what 0008 already covered), that should be requested
and run as its own numbered assessment rather than folded into this
closure note.
