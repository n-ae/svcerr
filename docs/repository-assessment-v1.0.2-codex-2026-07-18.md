---
title: Repository assessment — svcerr v1.0.2
date: 2026-07-18
reviewer: Codex
review_mode: Direct review; no delegation
reviewed_commit: 6c3b86d7852b64a463026a59eb99d4d240b3c0b9
release: v1.0.2
status: Complete
---

# Repository assessment — svcerr v1.0.2

## Verdict

`v1.0.2` is production-ready. This direct review found no critical, high,
medium, or low-severity defect, and no release blocker.

The release is a focused closure of the prior HEAD cross-review findings. Its
changes preserve the package's central properties: canonical error identity,
safe public projections, consistent HTTP classification, and recovery behavior
that remains honest after a response has been committed or a connection has
been hijacked.

| Severity | Count | Result |
|---|---:|---|
| Critical / high / medium / low | 0 | No actionable finding |

## Scope

Reviewed the root `github.com/n-ae/svcerr` module and the independently
versioned `github.com/n-ae/svcerr/zerologadapter` module at tag `v1.0.2`.
The review covered the release changes, error construction and classification,
JSON/HTML/RFC 9457 rendering, headers, logging, panic recovery, renderer
isolation, public documentation, tests, module boundaries, and GitHub Actions
configuration.

## Release-change assessment

The six items closed by `v1.0.2` are correctly addressed and are backed by
specific tests:

- Committed and hijacked panic logs now derive diagnostic fields from the
  recovered internal error, retain the stack trace, and avoid fabricating an
  `http_status` for a response that was not rendered.
- JSON rendering treats a nil-error but invalid marshal result as a rendering
  failure; error-valued marshaler panics retain `errors.Is`/`errors.As`
  identity through `%w`.
- Empty custom codes normalize to `INTERNAL_ERROR`, while global and
  instance-scoped status registries reject an empty key.
- Problem-details titles fall back to a meaningful per-code default when a
  caller maps an error to a nonstandard HTTP status with no standard reason
  phrase.
- CI and scheduled vulnerability scanning now declare read-only token
  permissions, scan both modules, and pin `govulncheck` to a reviewed version.

These are narrow changes in the appropriate ownership layers. In particular,
the renderer's immutable configuration continues not to read or mutate the
package-global configuration, while the compatibility-oriented package-level
API retains its documented behavior.

## Architecture and maintainability

- The dependency-free root module and nested zerolog adapter remain a strong
  boundary: consumers only acquire zerolog when they import the adapter.
- Capability-sized interfaces let custom errors participate without requiring
  the full built-in error hierarchy.
- Private semantic identity plus accessors keeps response details, headers,
  contexts, and log fields derived from one state rather than independently
  mutable copies.
- The response tracker and recovery middleware cover difficult `net/http`
  states—informational responses, implicit commitment, flushing, write
  failures, optional-interface discovery, and hijacking—with matching tests
  and clear README guidance.
- The production code is necessarily detailed for its HTTP surface, but its
  division across error model, rendering, headers, tracking, recovery, status,
  and logging remains cohesive. No structural refactor is warranted.

## Deliberate constraints, not findings

- Error presentation setters are intentionally not safe for concurrent
  mutation after an error has been shared; the documented lifecycle is
  construct, configure, then return.
- The root module supports Go 1.21; the adapter's dependency chain requires
  the higher floor declared in its own module. CI exercises each module at its
  declared floor and at stable Go.
- Context-aware logging APIs and additional semantic constructors remain
  sensible additive extensions only if consumer demand justifies their public
  API cost.

## Verification performed

On macOS arm64 with Go 1.26.5:

- `go test ./...` and `go test ./...` in `zerologadapter`
- `go vet ./...` in both modules
- `go test -race -count=1 ./...` in both modules
- `go test -count=1 -cover ./...` in both modules — 100.0% statement
  coverage each
- `GODEBUG=panicnil=1 go test -count=1 ./...` in both modules
- `gofmt -l .` — no output
- `GOWORK=off go mod tidy -diff` in both modules — no drift
- `govulncheck ./...` in both modules — no reachable vulnerabilities
- `git diff --check HEAD~1 HEAD` and a clean worktree before saving this
  assessment

The initial sandboxed test run could not create the loopback listener used by
one HTTP recovery test. The same suite passed unchanged with ordinary local
loopback access; this is an execution-environment restriction, not a project
failure.

## Recommendation

Release or adopt `v1.0.2` as-is. Make the next full review event-driven—for a
substantive feature, consumer report, dependency-floor change, or release
candidate—rather than refactoring stable code solely to create churn.
