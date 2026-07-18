---
title: Current repository assessment — svcerr
date: 2026-07-18
reviewer: Codex
review_mode: Direct review; no delegation
reviewed_commit: 8ef413c905257cd9b05111b99bd33c3916cb5fb9
release_baseline: v1.0.1
status: Complete
---

# Current repository assessment — svcerr

## Scope

This is a fresh, direct review of the repository at `HEAD`, four commits
after the `v1.0.1` tag. It covers:

- the root `github.com/n-ae/svcerr` module;
- the nested `github.com/n-ae/svcerr/zerologadapter` module;
- error identity, wrapping, classification, and public-message safety;
- JSON, HTML, and RFC 9457 response rendering;
- panic recovery and `http.ResponseWriter` capability preservation;
- global and instance-scoped configuration;
- logging, documentation, tests, CI, module boundaries, and release
  hygiene; and
- the post-tag assessment-index changes and scheduled vulnerability scan.

Earlier assessments were read as historical context after independently
examining the current implementation. There are no production-code
changes after `v1.0.1`; the only post-tag executable change is
`.github/workflows/govulncheck.yml`.

## Verdict

The repository is production-ready. I found no critical, high, or
medium-severity issue and no code-level defect. The v1 API has a coherent
source of error identity, conservative client-data boundaries, consistent
classification across body, status, headers, and logs, and unusually deep
coverage of `net/http` recovery behavior.

I found one low-severity operational maintainability issue in the new
vulnerability workflow:

| Severity | Count | Summary |
|---|---:|---|
| Critical / high / medium | 0 | No release blocker found |
| Low | 1 | The vulnerability workflow floats its scanner version and relies on an external token-permission default |

The workflow itself is valid and currently succeeds for both modules. The
finding concerns reproducibility and policy ownership, not present scan
correctness.

## Finding

### Low — L1: vulnerability-scan execution is not fully defined by the repository

`.github/workflows/govulncheck.yml` installs
`golang.org/x/vuln/cmd/govulncheck@latest` on every run. Vulnerability
results are intentionally time-varying because the advisory database
changes, but floating the scanner binary adds a second independent source
of change: the same commit and database state can be analyzed differently
after a new scanner release. That makes a future regression harder to
reproduce and review.

Neither workflow declares an explicit `permissions` block. The repository's
current GitHub Actions setting supplies read-only workflow permissions, so
there is no current write-capable token exposure. The least-privilege
contract nevertheless lives in mutable repository settings rather than
beside the workflow that depends on it. A copied repository, changed
organization default, or restored workflow in another context could silently
receive broader permissions.

Impact is low: the scheduled scan runs only trusted default-branch code, the
current token default is read-only, both matrix jobs have passed, and local
`govulncheck` 1.6.0 reports no reachable vulnerabilities. This does not
affect the library's runtime.

Recommendation:

1. Pin the scanner to a reviewed version, for example
   `govulncheck@v1.6.0`, and update it deliberately as part of dependency
   maintenance.
2. Add `permissions: contents: read` to both workflow files so least
   privilege is version-controlled.
3. For stronger supply-chain reproducibility, consider pinning action
   references to commit SHAs with automated update tooling; this is
   hardening, not required to close the finding.

## Architecture and maintainability

### Strong boundaries

- The root module has no third-party runtime dependencies. Zerolog support
  is isolated in an independently versioned nested module, so root
  consumers do not acquire an unwanted logging dependency.
- Capability interfaces (`Coder`, `StackTracer`, `PublicMessager`,
  `PublicDetailer`, and the RFC/header capabilities) keep custom error
  participation narrow and composable.
- `Renderer` offers immutable instance-scoped configuration while the
  package-level registry and setters remain a documented compatibility
  path. Configuration is copied and isolated between renderers.

### Consistent and safe projections

- Semantic identity fields are private and read through accessors.
  `Context`, response details, retry headers, and structured log fields
  derive from that canonical state.
- `outermostCoded` supplies one classification node for code, message,
  details, classification headers, and type-specific logging. An outer
  internal wrapper therefore cannot accidentally expose a wrapped
  client-facing identifier or retry hint.
- Database, upstream-service, and internal messages default to generic
  client text. Wrapped causes, validation values, SQL, and upstream URLs
  are not automatically rendered.
- JSON marshal failures are contained and completely reclassified to
  `INTERNAL_ERROR` under the active status mapping. The fallback discards
  classification-specific headers and reports both rendering and delivery
  failures.

### HTTP recovery

- The response tracker correctly distinguishes informational responses,
  final commitment, implicit writes, flushes, flush errors, and successful
  hijacks.
- Optional `Flusher`, `FlushError`, and `Hijacker` behavior is preserved
  only when supported, including discovery through `Unwrap` chains.
- A panic after commitment is logged and converted to
  `http.ErrAbortHandler`, avoiding a clean-looking truncated response.
  Hijacked connections remain owned by the handler and are diagnosed
  without a meaningless status value.
- Deliberately omitted optional interfaces (`http.Pusher` and
  `io.ReaderFrom`) and middleware-ordering consequences are documented in
  the README.

### Complexity posture

`errors.go` is the largest production file at 1,089 lines, but it remains a
cohesive definition of the public error model, constructors, accessors, and
classification helpers. The more stateful HTTP concerns are already split
across rendering, headers, tracking, recovery, status, and logging files.
Another mechanical split is not justified by the current change rate or
defect profile.

The test-to-production line ratio is high, but the tests target meaningful
contracts rather than implementation trivia: external-package API use,
error-chain ordering, information exposure, wire shapes, header cleanup,
custom writer failures, panic states, optional interfaces, global isolation,
and concurrency. One hundred percent statement coverage is therefore useful
corroboration, not the sole quality argument.

## Open design constraints, not findings

- `WrapValidationError` cannot record a validation value and
  `NewDatabaseError` cannot record a query. These are known constructor
  asymmetries retained pending consumer demand; additive v1 APIs remain
  available if that demand appears.
- Presentation setters, retry-hint mutation, and stack recapture are
  intentionally unsafe after an error is shared across goroutines. The
  construct-configure-return lifecycle is documented.
- Under legacy `GODEBUG=panicnil=1` semantics, literal `panic(nil)` cannot
  be distinguished through `recover` from `runtime.Goexit`; the middleware
  uses abnormal return detection and the full suite passes in that mode.
- The nested adapter currently requires Go 1.25 because of its dependency
  chain, while the dependency-free root module retains a Go 1.21 floor.
  CI tests both declared floors.

## Verification performed

Reviewed a clean worktree at
`8ef413c905257cd9b05111b99bd33c3916cb5fb9` using Go 1.26.5 on
darwin/arm64.

Successful local checks:

- `go test -count=1 -cover ./...` — 100.0% statements in both modules;
- `go test -race -count=1 ./...` — both modules;
- `go test -shuffle=on -count=5 ./...` — both modules;
- `go vet ./...` — both modules;
- `go build ./...` — both modules;
- `gofmt -l .` — no output;
- `golangci-lint` 2.12.2 — 0 issues in both modules;
- `GOWORK=off go mod tidy -diff` — no drift in either module;
- `GODEBUG=panicnil=1 go test -count=1 ./...` — both modules; and
- `govulncheck` 1.6.0 — no vulnerabilities found in either module.

Current GitHub Actions evidence:

- [CI run 29626623137](https://github.com/n-ae/svcerr/actions/runs/29626623137)
  passed all four root/adapter and floor/stable matrix jobs at reviewed
  `HEAD`.
- [govulncheck run 29626623390](https://github.com/n-ae/svcerr/actions/runs/29626623390)
  passed both module jobs at reviewed `HEAD`.
- Repository workflow-token defaults were verified as read-only, with pull
  request approval disabled.

`actionlint` was not installed locally. The new workflow's successful
GitHub-hosted execution validates its syntax, expressions, action inputs,
module matrix, and commands in the target environment.

## Recommended order

1. Version-control the workflows' read-only permission contract and pin the
   vulnerability scanner.
2. Otherwise preserve the current architecture; no broad refactor is
   warranted.
3. Make the next full review event-driven: a substantive feature, consumer
   report, dependency-floor change, or release candidate.
