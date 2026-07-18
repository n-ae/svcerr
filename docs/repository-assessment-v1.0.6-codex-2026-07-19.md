---
title: Repository assessment — svcerr v1.0.6
date: 2026-07-19
reviewer: Codex
review_mode: Direct review; no delegation
reviewed_commit: 0637ac7816fa866d5a39de1460162256dac54e09
baseline: v1.0.6; zerologadapter v1.0.4
status: Complete
---

# Repository assessment — svcerr v1.0.6

## Verdict

No actionable defects were found. The root module and its independently
versioned `zerologadapter` module are release-ready at the reviewed HEAD.
The typed-nil extension-point issue identified in the prior direct review is
closed: `isNilValue` now rejects every reachable nil-capable concrete kind
before a custom `Coder`, `StackTracer`, or stack-trace setter is invoked.

| Severity | Count | Summary |
|---|---:|---|
| Critical / high | 0 | No security, data-exposure, or response-corruption defect found |
| Medium | 0 | No reliability or API-contract defect found |
| Low | 0 | No actionable maintainability issue found |

## Scope

Reviewed root module `github.com/n-ae/svcerr` and nested module
`github.com/n-ae/svcerr/zerologadapter` at
`0637ac7816fa866d5a39de1460162256dac54e09`. The review covered error-chain
classification, typed-nil handling, response renderers and their fallback
paths, header policies, RFC 9457 output, logging isolation, panic recovery
and response-writer capability tracking, configuration concurrency, module
boundaries, and the adapter.

## Findings

None.

The former typed-nil gap is fully addressed. `isNilValue` checks pointers,
slices, maps, channels, functions, and interfaces; the current regressions
exercise all reachable kinds that can reach the public extension interfaces.
The guard is used consistently by classification, stack-trace extraction, and
stack-trace recapture, so an unusable typed-nil custom error degrades to the
safe internal-error behavior instead of calling a method on its nil receiver.

## What is working well

- Classification, public details, retry/authentication headers, and log
  context all originate from the same outermost coded error node, preventing
  wrapper/inner-error mismatches and accidental detail leaks.
- JSON and problem-details rendering marshal before committing a response and
  safely replace failures with fixed internal-error bodies. Panicking custom
  marshalers and short writes are reported without escaping the writer path.
- Recovery deliberately distinguishes uncommitted, committed, flushed, and
  hijacked responses, avoiding a second response body after a panic.
- Root logging remains dependency-free; the zerolog integration is correctly
  isolated in its nested module and tracks the root v1.0.6 requirement.
- Global package compatibility settings use locks, while `Renderer` snapshots
  its configuration for concurrent, cross-service-safe use.

## Verification

All checks passed on darwin/arm64 with Go 1.26.5:

- `go test ./...` in the root module;
- `go test ./...` in `zerologadapter`;
- `go vet ./...` and `go mod tidy -diff` in both modules;
- `go test -race -count=1 ./...` in both modules;
- `go test -count=1 -cover ./...` in both modules — 100.0% statement
  coverage in each;
- `gofmt -l *.go zerologadapter/*.go` and `git diff --check`.

The ordinary sandbox prevents the real-server recovery test from binding its
IPv6 loopback listener. The unchanged suite, race suite, and coverage suite
all passed when granted local-loopback permission; this is an environment
restriction, not a test failure.

## Recommended next step

No corrective change is required. Retain the current focused regression
coverage when making future changes to the open custom-error interfaces.
