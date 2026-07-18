---
title: Repository assessment — svcerr v1.0.4 and current HEAD
date: 2026-07-18
reviewer: Codex
review_mode: Direct repository review; no delegation
reviewed_commit: fe5a7af128097fe31ac7df836c4747a74e4d8d93
baseline: v1.0.4 root module; zerologadapter/v1.0.2; clean worktree
status: Complete
---

# Repository assessment — svcerr v1.0.4 and current HEAD

## Verdict

The repository remains well designed and unusually thoroughly tested for a
small Go library. Its separation of the dependency-free core from the nested
zerolog adapter, immutable `Renderer` option, centralized error projection,
and cautious HTTP recovery behavior are all sound.

One medium-severity robustness issue remains in the documented custom-error
extension surface. The prior direct assessment identified it before v1.0.4;
the v1.0.4 change closed the pointer-backed cases, but did not close the
broader non-pointer typed-nil case. Accordingly, the assessment-index claim
that M1 is closed is premature.

| Severity | Count | Summary |
|---|---:|---|
| Critical / high | 0 | No security, confidentiality, or data-integrity issue found. |
| Medium | 1 | Nil non-pointer custom `Coder` values can panic error classification. |
| Low | 0 | — |

## Finding

### M1 — `isNilValue` excludes typed-nil custom `Coder` values other than pointers

`outermostCoded` correctly asks `isNilValue` before calling a matched
`Coder`, but [`errors.go`](../errors.go) limits that helper to
`reflect.Pointer`. That is sufficient for svcerr's built-in errors but not
for the explicitly supported minimal custom `Coder` interface.

A named slice, map, function, channel, or interface type can implement both
`error` and `Coder`, be held as a non-nil `error` interface while its concrete
value is nil, and panic when `Code` dereferences it. For example:

```go
type codedSlice []svcerr.ErrorCode

func (e codedSlice) Error() string         { return "coded" }
func (e codedSlice) Code() svcerr.ErrorCode { return e[0] }

var err error = codedSlice(nil)
_ = svcerr.GetErrorCode(err) // panics
```

This reaches `GetErrorCode`, all response writers, and logging. It is a
reliability issue in consumer-supplied extension code rather than an
untrusted-input vulnerability, but the package promises that a custom type
need only implement `Coder` to participate in classification.

Recommendation: change `isNilValue` to first reject an invalid
`reflect.Value`, then return `IsNil()` for every nil-capable kind:
`Pointer`, `Map`, `Slice`, `Func`, `Chan`, and `Interface`. Preserve the
existing internal-error fallback. Add regression coverage for at least nil
slice- and map-backed custom `Coder` values through `GetErrorCode` and one
writer. Update the helper comment, which currently states that pointer-only
handling is intentional.

## Strengths

- Error code, public details, status, headers, and log fields all derive from
  the same outermost coded error, avoiding accidental metadata leaks from a
  wrapped cause.
- Response writing has a single finalization path, explicit header policy,
  short-write detection, JSON marshal containment, and useful `WriteResult`
  reporting.
- Recovery tracks commitment carefully across informational responses,
  flushing, write failures, and hijacking; it safely aborts instead of
  appending a second response after commitment.
- `Renderer` snapshots configuration and avoids the package globals, while
  compatibility package functions retain the established global behavior.
- The root module has no dependencies; the zerolog adapter is correctly a
  separate nested module and is independently checked in CI.
- CI covers each module at its declared Go floor and stable Go, including
  stable race detection, and schedules `govulncheck`.

## Verification

Reviewed source, public documentation, release/tag history, tests, module
metadata, CI, and both Go modules at clean HEAD `fe5a7af` on Go 1.26.5
(`darwin/arm64`). The following passed:

- focused root rendering, error-extraction, and contract tests;
- `go test ./...` for `zerologadapter`;
- `go vet ./...` in both modules;
- `gofmt -l .`, `git diff --check`, and `GOWORK=off go mod tidy -diff` in
  both modules.

The unrestricted full root suite could not be confirmed in this environment:
`TestRecoveryMiddlewareSurvivesATypedNilCoderThroughARealServer` uses
`httptest.Server`, and the sandbox rejects its loopback listener with
`bind: operation not permitted`. The failure is environmental, not an
assertion failure. The test should be run in CI or a local environment with
ordinary loopback access.

## Recommended next step

Close M1 in the next root patch release, correct the historical assessment
index wording, and otherwise retain the current architecture. No broad
refactor is warranted.
