---
title: Direct repository assessment — svcerr v1.0.0
date: 2026-07-17
reviewer: Codex
review_mode: Direct review; no delegation and no maintainable-architect-v* agent
reviewed_commit: 6abc6e465db014594df647b378c5d73d1e606aaf
reviewed_releases:
  - v1.0.0
  - zerologadapter/v1.0.0
status: Complete
---

# Direct repository assessment — svcerr v1.0.0

## Scope

This is a fresh review of the current repository, performed directly by
Codex without delegation or a specialized maintainable-architect agent. It
covers:

- the root `github.com/n-ae/svcerr` module;
- the nested `github.com/n-ae/svcerr/zerologadapter` module;
- the public error model and capability interfaces;
- HTTP JSON, HTML, and RFC 9457 rendering;
- panic recovery and `http.ResponseWriter` capability preservation;
- global and instance-scoped configuration;
- structured logging, documentation, tests, CI, and module hygiene; and
- earlier assessments as historical context only, after independently
  reading and testing the current implementation.

`HEAD` is the `zerologadapter/v1.0.0` tag. The root `v1.0.0` tag points at
the preceding assessment commit; the only later code change is the adapter
module's requirement bump from the root release candidate to `v1.0.0`.

## Verdict

The repository is production-ready and unusually strong for a small Go
library. Its dependency boundary is clean, its error identity is now
canonical, response projections consistently use the outermost coded error,
global configuration has a well-designed instance-scoped alternative, and
the subtle `net/http` recovery state machine is extensively documented and
tested.

I found no critical, high, or medium-severity defect. I found two
low-severity issues:

| Severity | Count | Summary |
|---|---:|---|
| Critical / high / medium | 0 | No release-blocking issue found |
| Low | 2 | Marshal fallback bypasses an `INTERNAL_ERROR` status override; small public-documentation drift |

The first finding is a real configuration-composition inconsistency, but its
fallback remains safe: it sends a generic 500 document, reports the marshal
error, and does not leak the unencodable value.

## Findings

### Low — L1: marshal fallback bypasses the configured `INTERNAL_ERROR` status

`RendererConfig.StatusCodes` promises that built-in codes can be overridden
(`renderer.go:13-18`), and normal rendering plus panic recovery consult the
renderer-local mapping. The package-level equivalent,
`RegisterStatusCode`, makes the same promise.

The JSON marshal-failure path instead assigns
`http.StatusInternalServerError` directly (`render.go:198-202`). The
problem-details path does the same (`render.go:406-410`). Both fallback
bodies identify themselves as `INTERNAL_ERROR`, so the emitted code and the
configured mapping for that code can disagree.

A focused throwaway probe, removed after execution, configured:

```go
r, _ := svcerr.NewRenderer(svcerr.RendererConfig{
	StatusCodes: map[svcerr.ErrorCode]int{
		svcerr.ErrCodeInternal: http.StatusServiceUnavailable,
	},
})

err := svcerr.NewNotFoundError("widget", "42")
err.SetPublicDetail("unencodable", make(chan int))
```

Both `r.JSON` and `r.Problem` returned and wrote status 500 while their
bodies carried `INTERNAL_ERROR`; the configured status for that exact code
was 503. Ordinary internal errors and the renderer's recovery middleware do
honor the 503 override.

Impact is low because this requires two opt-in edges at once: a non-default
internal mapping and a public detail that cannot be JSON-encoded. The result
is still a valid, generic server-error response. It can nevertheless defeat
an application's deliberate 503/retry semantics, and it weakens the
otherwise strong invariant that code, status, body, and headers derive from
one classification.

Recommendation: model fallback as a complete reclassification to
`ErrCodeInternal`, then derive its status through the active settings
(`s.status(ErrCodeInternal)`) and derive its body and classification headers
from that same fallback plan. Add table tests for JSON and problem output
through both a `Renderer` override and `RegisterStatusCode`.

### Low — L2: public documentation retains pre-v1/pre-split references

Three small public-facing references have drifted:

1. `README.md:222-224` links the HTTP status mapping to `http.go`, which no
   longer exists after the v0.11.0 responsibility split. The implementation
   is now in `status.go`.
2. The custom `PublicDetailer` and `Logger` snippets still spell their maps
   as `map[string]interface{}` (`README.md:304-306`,
   `README.md:510-513`), while the v1 API and migration notes consistently
   use `map[string]any`. These spellings are type-identical and still compile,
   but the mismatch makes the v1 documentation look partially migrated.
3. `logger.go:17-18` says the panic path is an example where `err` is nil.
   Recovery actually constructs and passes an `InternalError` for every
   recovered panic. Nil remains valid when a caller renders a nil error, so
   the contract is sound; only the example is stale.

Recommendation: point the README at `status.go`, use the public API's `any`
spelling in snippets, and replace the nil-error example with an actual call
path.

## Architecture and maintainability

### What is working especially well

- **One identity source.** Semantic fields are private and exposed through
  read-only accessors. `Context`, response details, headers, and log fields
  derive from the same state, eliminating the pre-v1 desynchronization class.
- **One classification node.** `outermostCoded` controls code, message,
  details, headers, and safe built-in log context. An outer internal wrapper
  cannot accidentally inherit client-visible fields from an inner error.
- **Conservative client safety.** Database, external-service, and internal
  messages default to generic client text. Wrapped causes, validation values,
  SQL text, and upstream URLs are not automatically rendered.
- **Cohesive rendering pipeline.** Format-specific body construction is
  separate from shared header cleanup, classification headers, commitment,
  and checked delivery in `finalizeErrorResponse`.
- **Sound configuration direction.** `Renderer` provides immutable,
  concurrent, instance-scoped configuration while package globals remain a
  convenient compatibility layer. Renderer configuration is copied and does
  not leak across instances or into global writers.
- **Careful HTTP recovery.** The tracker handles informational responses,
  implicit and explicit commitment, flush and `FlushError`, hijacking,
  nested `Unwrap` chains, invalid status panics, partial writes, and
  post-commit panics. The deliberate omission of `http.Pusher` and
  `io.ReaderFrom` is documented rather than accidental.
- **Clean dependency boundary.** The root module has no third-party
  dependencies. Zerolog is isolated in an independently versioned nested
  module.
- **Strong consumer-facing tests.** Internal state-machine tests are
  complemented by `package svcerr_test` contract tests and compiled examples.

### Remaining design constraints, not counted as findings

- `WrapValidationError` cannot carry a non-nil validation value, and
  `NewDatabaseError` cannot carry a query. This v1 constructor narrowing is
  explicitly recorded in the release tag and can be addressed additively if
  consumers need it.
- Presentation setters and `ExternalAPIError.SetRetryAfter` mutate errors in
  place and are intentionally not safe once an error is shared. The package
  and README document the construct-configure-return lifecycle.
- Recovery cannot preserve every optional writer interface. Its supported
  capability set and middleware-ordering trade-offs are documented in enough
  detail for consumers to make an informed choice.
- `errors.go` remains the largest production file at 1,078 lines, but its
  contents are cohesive and much of the size is API documentation. The more
  complex HTTP responsibilities have already been split into focused files;
  another mechanical split is not currently justified.

## Test and release posture

The suite reaches 100.0% statement coverage in both modules. That number is
supported by meaningful behavioral coverage rather than trivial line
execution: tests cover error-chain ordering, public-message boundaries,
reserved problem members, marshal and delivery failures, configuration
isolation, writer capability combinations, response commitment, panic
recovery, and external-package contracts.

CI tests both modules at their declared Go floor and at stable Go, with
formatting and lint on stable and a stable race-detector lane. The latest run
for reviewed `HEAD` succeeded in all four matrix jobs, including both floor
jobs:

`https://github.com/n-ae/svcerr/actions/runs/29589721464`

The independently tagged root and adapter releases are aligned at v1, and
the adapter's `go.mod` now requires root `v1.0.0`.

## Verification performed

Reviewed a clean worktree at
`6abc6e465db014594df647b378c5d73d1e606aaf` using Go 1.26.5 on
darwin/arm64.

Successful checks:

- `go test ./...` — root and adapter;
- `go test -race -count=1 ./...` — root and adapter;
- `go test -shuffle=on -count=5 ./...` — root and adapter;
- `go vet ./...` — root and adapter;
- `gofmt -l .` — no output;
- `golangci-lint` v2.12.2 — 0 issues in both modules;
- `go test -coverprofile=... ./...` — 100.0% statements in both modules;
- `GOWORK=off go mod tidy -diff` — no drift in either module;
- Go 1.21.13 root `go build` and `go vet`;
- Go 1.25.0 adapter `go test` and `go vet`;
- latest GitHub Actions matrix — all four jobs successful; and
- a focused, removed regression probe confirming L1 for both JSON formats.

The root Go 1.21.13 test binary could not execute locally because the old
toolchain's darwin binary hit `dyld: missing LC_UUID load command`. The same
source built and vetted under 1.21.13, and the reviewed CI run passed the root
1.21 floor test on Linux. This is treated as a local toolchain/runtime
limitation, not a repository failure.

Standalone `staticcheck` was not counted because the installed 2025.1.1
binary was built with Go 1.25 and cannot analyze the local Go 1.26 standard
library. Golangci-lint's current Go 1.26 build includes Staticcheck and
reported no issues. A separate vulnerability-database scan and post-tag
module-proxy download test were not performed.

## Recommended order

1. Make marshal fallback honor the active `INTERNAL_ERROR` mapping and add
   composition tests.
2. Correct the three small documentation references.
3. Otherwise preserve the current architecture; no broad refactor is
   warranted by this review.

