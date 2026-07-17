---
title: Direct repository assessment — svcerr v0.6.4
date: 2026-07-17
reviewer: Codex
review_mode: Direct review; no delegation and no maintainable-architect-v* agent
reviewed_commit: 07c91b0fffea2c7ccb86281d6479f549d46ff768
reviewed_release: v0.6.4-2-g07c91b0
status: Complete
---

# Direct repository assessment — svcerr v0.6.4

## Scope

This is a fresh review of the current repository, performed directly by
Codex without delegation or a specialized maintainable-architect agent. It
covers:

- the root `github.com/n-ae/svcerr` module;
- the nested `github.com/n-ae/svcerr/zerologadapter` module;
- public API and error-model consistency;
- HTTP response rendering and panic recovery;
- structured logging behavior;
- tests, CI, module hygiene, and user-facing documentation; and
- the prior assessments only as historical context, not as substitutes for
  reading and testing the current code.

The reviewed `HEAD` is two commits beyond the root `v0.6.4` tag. Those commits
only bump the adapter's root-module requirement and add the prior closure
assessment; the root package implementation is the published v0.6.4 code.

## Verdict

The repository is in good shape and is suitable for continued use. It has a
cohesive purpose, a sensible dependency boundary, unusually strong tests, and
clear handling of several subtle `net/http` behaviors. The fixes closed by
v0.6.4 are present and exercised: response-controller capability discovery
follows `Unwrap` chains, invalid `WriteHeader` panics are not pre-recorded as
commitment, HTML preserves a rate-limit `Retry-After`, and short writes become
`io.ErrShortWrite`.

I found no critical or high-severity defect. I found one medium maintainability
and correctness issue, plus three low-severity issues:

| Severity | Count | Summary |
|---|---:|---|
| Critical / high | 0 | No release-blocking issue found |
| Medium | 1 | Public mutable fields duplicate private constructor state and can violate invariants |
| Low | 3 | Incomplete built-in log context, RFC 9457 member collision, documentation drift |

The main issue is architectural rather than a regression: each semantic error
stores the same facts in multiple places. The private `BaseError` state is
captured at construction, while public fields remain writable. A later field
assignment can therefore make the error code, message, `Context`, response
details, response headers, and structured log describe different events.

## Findings

### Medium — M1: public semantic fields are a second, mutable source of truth

`BaseError` privately stores `code`, `message`, and a `context` map
(`errors.go:166-184`). Each concrete error then exposes related public fields:
`ValidationError.Field`, `DatabaseError.Operation`, `AuthenticationError.Reason`,
`NotFoundError.ResourceID`, `RateLimitError.RetryAfter`, and so on. Constructors
populate both representations once, but no setter keeps them synchronized.

The package documentation warns only against *concurrent* mutation
(`errors.go:20-29`, `README.md:375-383`). It does not say sequential mutation
of the public fields is unsupported. One public field is explicitly designed
for post-construction assignment:
`ExternalAPIError.RetryAfter` says to assign it directly when known
(`errors.go:577`).

A focused temporary test reproduced the concrete protocol consequence:

```go
err := svcerr.NewRateLimitError("api", 100, 60)
err.RetryAfter = -5

w := httptest.NewRecorder()
svcerr.WriteJSON(w, err)
```

The resulting state is inconsistent:

```text
Retry-After header:       -5
JSON details.retry_after: -5
Context()["retry_after"]: 60
```

`-5` is not a valid `Retry-After` delay: RFC 9110 defines `delay-seconds` as a
non-negative decimal integer. The constructor's clamp is therefore not an
enforced invariant; it is only an input cleanup that a normal public-field
assignment can bypass.

The same duplicated-state pattern affects other types even where it does not
produce an invalid header:

- changing `AuthenticationError.Reason` does not recompute its private code,
  so a `TOKEN_EXPIRED`/401 response can be logged with
  `auth_reason=permission_denied`;
- changing `NotFoundError.ResourceID` changes response details and log fields,
  but not the already-built message or `Context`;
- changing `RateLimitError.Limit` or `RetryAfter` changes response details but
  leaves the constructor snapshot returned by `Context` unchanged.

Recommended near-term changes:

1. Clamp `RateLimitError.RetryAfter` again at the wire boundary in
   `rateLimitRetryAfterHeader` and when extracting its public details. Add a
   regression test that mutates the public field after construction.
2. Document whether concrete fields are immutable constructor outputs. If
   mutation is supported, add setters that update every derived
   representation and direct callers toward them.
3. Add consistency tests for code, message, context, details, headers, and log
   fields after every supported mutation path.

Recommended longer-term direction for a breaking release: keep one canonical
classification/state object, make semantic fields private with getters, and
derive response and log projections from that state. In particular, replace
the direct-assignment contract of `ExternalAPIError.RetryAfter` with a setter
or constructor option.

### Low — L1: structured logging omits useful context for three built-in types

`errorLogFields` has explicit cases for validation, database, external API,
authentication, and not-found errors (`http.go:706-719`). It has no cases for:

- `ConflictError` (`resource_type`, `conflict_key`);
- `RateLimitError` (`service`, `limit`, `retry_after`); or
- `InternalError` (`component`).

A focused probe confirmed those keys are absent. With the bundled zerolog
adapter, for example, `NewInternalError("billing", "charge failed")` logs the
error text, code, status, and stack trace, but not `component=billing`. That
field is especially useful for diagnosing a 5xx response.

This is also a maintenance signal: adding a built-in error requires updating
constructors, public detail extraction, message policy, status mapping, and a
separate hand-written logging switch. High line coverage does not ensure all
those projections remain complete.

Do not blindly merge `BaseError.Context()` into logs: it intentionally contains
values such as validation input, SQL text, and external URLs that may be
sensitive. Instead, use an explicit safe-log-field projection and cover every
built-in type in one table-driven completeness test. An unexported
`logFielder` capability implemented by built-in types would keep ownership of
safe diagnostic fields near each type.

### Low — L2: public details can occupy an omitted RFC 9457 member

`ProblemDetails.MarshalJSON` first copies every `Extensions` entry into the
output map (`http.go:375-379`). It then overwrites `type`, `title`, `status`,
and `code`, but writes optional `detail` and `instance` only when their typed
fields are non-empty (`http.go:383-388`).

Consequently, a public-detail key can become a registered problem-details
member rather than an extension:

```go
err := svcerr.NewNotFoundError("widget", "42")
err.SetPublicDetail("instance", 123)

w := httptest.NewRecorder()
svcerr.WriteProblem(w, err)
```

The emitted object contains:

```json
{"instance":123}
```

RFC 9457 defines `instance` as a JSON string containing a URI reference and
requires consumers to ignore a registered member with the wrong value type.
The output is therefore syntactically valid JSON but semantically unusable as
an RFC 9457 `instance`.

Reserve `type`, `title`, `status`, `detail`, `instance`, and the package-owned
`code` name unconditionally when copying extensions. If an optional typed
member is empty, omit it rather than allowing an extension with that name to
take its place. Add collision tests for every reserved name, including direct
`ProblemDetails` marshaling and the `SetPublicDetail` writer path.

### Low — L3: two documentation pointers no longer describe the implementation

Two small comments have drifted:

1. `README.md:190-192` sends readers to the package comment in `errors.go` for
   the full code list and HTTP mapping. That package comment lists semantic
   types but not the mapping; the mapping lives in `HTTPStatusCode`
   (`http.go:53-106`).
2. The comment above `defaultMessageForCode` (`http.go:569-573`) says the
   function is also used as the RFC 9457 title. `WriteHTTPProblem` actually
   uses `http.StatusText(statusCode)` and an optional `ProblemTitler`
   (`http.go:434-446`), which is the correct behavior.

These do not affect runtime behavior, but both sit on public concepts that
have changed repeatedly. Correcting them reduces the chance that a future
change follows stale design guidance.

## Architecture and maintainability

### What is working well

- The root module remains dependency-free. Zerolog and its transitive
  dependencies are isolated in a separately versioned nested module.
- `outermostCoded` gives status, message, details, response headers, and
  built-in log metadata one classification node. This avoids crossing an
  outer error's code with an inner error's client-visible data.
- Public versus internal messages are conservative and well documented.
  Operational categories default to generic client text.
- Response rendering marshals before commitment, sanitizes representation
  headers, provides safe fallbacks, reports render and write failures, and
  detects non-conforming short writes.
- Recovery handles informational headers, normal writes, flushes,
  `FlushError`, hijacks, nested `Unwrap` chains, committed-response panics,
  write failures, `http.ErrAbortHandler`, nil loggers, and panicking loggers.
- CI covers both modules at their declared Go floor and the current stable Go
  release. The nested-module boundary is explained in the workflow.
- The source documents deliberate compromises such as shallow context copies,
  dropped optional writer interfaces, mutable errors, and compression-wrapper
  ordering.

### Main complexity hotspot

`http.go` is 1,190 lines and `http_test.go` is 2,303 lines. Much of that size is
justified by the response-writer capability matrix, but status selection,
classification, rendering, header policy, logging, and recovery are now
interleaved in one production file.

The three renderers also repeat the same sequence:

1. classify the error;
2. construct and render a body;
3. reset headers;
4. restore classification-specific headers;
5. write the status and body; and
6. report render/write errors.

The previously fixed HTML `Retry-After` omission is evidence that those copies
can drift. A private response plan/finalization helper could centralize steps
3-6 while leaving body construction format-specific. Independently of that
refactor, splitting the file into status mapping, rendering, logging, response
tracking, and recovery units would make ownership easier to see without
changing the public API.

### Test architecture

The test suite is exceptionally thorough by statement coverage, but every
root test uses `package svcerr`, and there are no compiled `Example...`
functions. Internal-package tests are appropriate for the response-writer
state machine, but they can use private helpers and therefore do not fully
exercise the package as an external consumer sees it.

Add a small `package svcerr_test` contract suite for:

- construction and `errors.Is`/`errors.As`;
- JSON, HTML, and problem-details output through only exported APIs;
- custom capability interfaces; and
- the README's primary usage examples as compiled examples.

The local race run is clean, but CI currently runs only ordinary tests. A
stable-toolchain `go test -race ./...` lane for both modules would continuously
protect the global status registry and future shared-state changes.

## Recommended order

1. In the next patch, clamp `RetryAfter` at emission and document the
   mutability contract (M1).
2. Add safe structured fields for conflict, rate-limit, and internal errors,
   with a completeness table test (L1).
3. Filter reserved problem-details member names from extensions (L2).
4. Correct the two stale documentation references (L3).
5. Add external-package contract/examples and a stable CI race lane.
6. Before a v1 API, decide whether semantic errors are immutable values or
   mutable objects, then remove the current duplicated-state ambiguity.
7. Split the HTTP implementation by responsibility and centralize common
   response finalization when doing so can be covered without changing wire
   behavior.

None of these recommendations requires blocking current v0.6.4 consumers that
treat constructed error fields as immutable.

## Verification performed

Reviewed a clean `main` worktree at
`07c91b0fffea2c7ccb86281d6479f549d46ff768`
(`v0.6.4-2-g07c91b0`) using Go 1.26.5.

Successful checks:

- `go test -count=1 ./... ./zerologadapter/...`
- `go test -race -count=1 ./... ./zerologadapter/...`
- `go test -shuffle=on -count=5 ./... ./zerologadapter/...`
- `go vet ./... ./zerologadapter/...`
- `GOWORK=off go mod tidy -diff` in both modules: no diff
- `gofmt -l .`: no output
- `git diff --check`: clean
- `golangci-lint` v2.12.2 in both modules: 0 issues
- root coverage: 99.8% of statements
- zerolog adapter coverage: 100.0% of statements

Focused temporary tests reproduced M1, L1, and L2. The probe file was removed
afterward and is not part of this assessment.

Not independently rerun here:

- the Go 1.20 root and Go 1.25 adapter floor toolchains;
- published-module proxy consumption;
- vulnerability-database scanning; or
- performance benchmarks.

The repository's CI configuration has floor and stable jobs for both modules,
and assessment 0009 records separate tag/proxy verification, but those are
not represented above as work performed by this direct review.

## Standards references

- [RFC 9110 §10.2.3 — Retry-After](https://www.rfc-editor.org/rfc/rfc9110.html#section-10.2.3)
- [RFC 9457 §3.1 — Problem Details members](https://www.rfc-editor.org/rfc/rfc9457.html#section-3.1)
- [RFC 9457 §3.2 — Extension members](https://www.rfc-editor.org/rfc/rfc9457.html#section-3.2)
