---
title: Current repository assessment — svcerr
date: 2026-07-18
reviewer: Codex
review_mode: Direct review; no delegation
reviewed_commit: 171d77a3faac6a5cbecd415f89fc76755a806fd7
baseline: v1.0.3 plus the untagged adapter requirement update
status: Complete
---

# Current repository assessment — svcerr

## Verdict

The repository is in strong shape: both modules are clean, formatted,
dependency-consistent, and comprehensively tested. The current HEAD should
not be treated as a fully clean release candidate, however, because the
recent typed-nil `Coder` hardening is incomplete for the explicitly supported
custom-error extension surface.

| Severity | Count | Summary |
|---|---:|---|
| Critical / high | 0 | No security or data-exposure defect found |
| Medium | 1 | A typed-nil custom `Coder` backed by a slice, map, function, channel, or interface can still panic classification |
| Low | 0 | — |

## Scope

Reviewed the root `github.com/n-ae/svcerr` module and the nested
`github.com/n-ae/svcerr/zerologadapter` module at HEAD
`171d77a3faac6a5cbecd415f89fc76755a806fd7`. The review covered the
post-v1.0.3 changes, error-chain classification, public rendering paths,
recovery, configuration, logging boundaries, tests, module metadata, and CI
workflows.

## Finding

### M1 — `outermostCoded` protects only nil pointers, not all typed-nil custom errors

`errors.go:1089` calls `isNilValue` after `errors.As` finds a `coderError`,
but `isNilValue` at `errors.go:1104` returns true only for a nil
`reflect.Pointer`. The package deliberately permits an external error to
implement only `Coder`; it does not require a pointer-backed struct. A named
slice, map, function, channel, or interface type can implement both `error`
and `Coder`, be stored as a non-nil `error` interface while its concrete value
is nil, and panic when its `Code` method reads that nil value.

For example, this is valid consumer code and panics in `GetErrorCode`:

```go
type codedSlice []svcerr.ErrorCode

func (e codedSlice) Error() string { return "coded" }
func (e codedSlice) Code() svcerr.ErrorCode { return e[0] }

var err error = codedSlice(nil)
_ = svcerr.GetErrorCode(err) // panic: index out of range
```

The same path is used by the JSON, HTML, and problem renderers. A direct
writer call therefore panics instead of producing the intended normalized
internal-error response; middleware may recover it only when the caller has
installed that separate layer. This is a reliability defect rather than an
untrusted-input exploit, but it is in a public, documented customization
surface and is the remaining part of the typed-nil issue v1.0.3 intended to
close.

Recommendation: make `isNilValue` return true for every nil-capable kind
(`Pointer`, `Map`, `Slice`, `Func`, `Chan`, and `Interface`) after checking
`Value.IsValid()`. Keep the existing fallback to `ErrCodeInternal`, and add
regressions for at least a nil slice-backed and nil map-backed custom
`Coder` through `GetErrorCode` and one response writer. The helper comment
should then describe the full supported set rather than pointer-only usage.

## What is working well

- The root module remains dependency-free, while zerolog support is properly
  isolated in the nested module.
- Error identity and public projections remain centralized: classification,
  body, headers, and logs derive from the same outermost coded node.
- Recovery continues to handle difficult `net/http` states deliberately,
  including informational responses, flushes, failed writes, committed
  responses, and hijacked connections.
- The package-level compatibility configuration and immutable `Renderer`
  configuration are cleanly separated.
- CI pins the vulnerability scanner, declares read-only permissions, tests
  both modules at their floor and stable Go versions, and runs race checks on
  stable.
- The adapter requirement bump to root `v1.0.3` is consistent with the
  documented independently versioned-module policy; the adapter itself is
  correctly left untagged until an adapter release is wanted.

## Verification

All successful checks below were run against the reviewed clean worktree on
darwin/arm64 with Go 1.26.5:

- `go test ./...` and `go vet ./...` in the root module;
- `go test ./...`, `go vet ./...`, and `go mod tidy -diff` in
  `zerologadapter`;
- `go test -race -count=1 ./...` in both modules;
- `go test -count=1 -cover ./...` in both modules — 100.0% statement
  coverage in each;
- `gofmt -l .`, `GOWORK=off go mod tidy -diff` in both modules,
  `go build ./...` in both modules, and `git diff --check` — all clean.

The sandbox initially blocked the recovery integration test's loopback
listener. The unchanged suite passed with ordinary local-loopback permission,
so this was an environment restriction rather than a repository failure.

## Recommended next step

Fix M1 before the next root patch release, then retain the present
architecture. No broad refactor is warranted.
