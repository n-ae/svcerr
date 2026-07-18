# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v1.0.1 (tag @ `f5667c6`; HEAD `4566a33` adds only the prior assessment's closure addendum)
**Date:** 2026-07-18
**Reviewer:** maintainable-architect-v4
**Prior assessment (this series):** [docs/assessment-maintainable-architect-v4-v1.0.0.md](assessment-maintainable-architect-v4-v1.0.0.md) (v1.0.0 + closure addendum)

---

## Disclosure

This is the strongest-bias review in the series: the v1.0.1 change set
was designed, implemented, and pre-verified by the same tooling in the
same working session, implementing the previous assessment's own
priority list — the reviewer is grading its own homework twice over.
Compensation, as always, is adversarial: three fresh falsification
probes (removed afterward, tree left clean) written specifically against
the *new* code's claims, including one aimed at the recovery mechanism
`safeJSONMarshal` itself — the kind of edge an implementer satisfied
with their fix is most likely to skip.

## Verdict

**v1.0.1 is sound.** All four v1.0.0 findings are verifiably closed at
the tag, no probe falsified a claim of the new code, and no new finding
of any severity emerged. Both modules hold 100.0% statement coverage,
race-, shuffle-, vet-, gofmt-, and golangci-lint-clean, `go mod tidy`
drift-free; CI is green on all four matrix lanes (including the Go 1.21
Linux floor) for both the tagged commit and HEAD, and
`svcerr@v1.0.1` resolves through proxy.golang.org.

| Severity | Count | Summary |
|---|---:|---|
| Critical / high / medium / low | 0 | — |
| Notes | 2 | panicnil GODEBUG nuance; hijacked log-field contract change |
| Open by design | 2 | Constructor narrowing (v1.0.0 carry); adapter `any` spelling unreleased |

## Closure verification at the tag

Each v1.0.0 finding re-checked against `f5667c6` source and behavior,
not against the closure addendum's word:

- **L1 (fallback reclassification)** — `writeJSONErrorBody` and
  `writeProblemJSONBody` both resolve `s.status(ErrCodeInternal)` and
  nil out the classification node; `finalizeErrorResponse` lost the
  `fallback` parameter entirely, so the invariant is structural rather
  than per-call-site. `TestMarshalFallbackHonorsConfiguredInternalStatus`
  and `TestMarshalFallbackDropsClassificationHeaders` pin both
  configuration layers, both JSON formats, Retry-After suppression, and
  the 401-mapping challenge nuance this series flagged.
- **L2 (marshaler panic containment)** — `safeJSONMarshal` guards both
  JSON body builders; `TestWriteContainsPanickingMarshaler` pins the
  no-panic + `RenderErr` + valid-fallback-body contract.
- **L3 (hijack diagnostics/ownership)** — `hijacked` recorded in
  `commitOnHijack`, distinct log message with `hijacked=true` and both
  zero-valued status fields dropped, ownership documented in source and
  README, regression tests pin the log shape and the untouched conn.
- **D1 + D2** — all wording present at the tag (package doc, `Value()`,
  README identity paragraph, `status.go` link, `any` spellings,
  `logger.go` example, `Renderer.Middleware` doc).

## Probes and what held

- **`panic(nil)` inside a custom marshaler.** The adversarial case for
  `safeJSONMarshal`: under pre-1.21 panicnil semantics, `recover()`
  returned nil for it, which would have skipped the conversion and
  returned a nil body with no error. Under this module's Go 1.21 floor
  the runtime delivers `*runtime.PanicNilError`; observed
  `RenderErr = "svcerr: JSON marshaler panicked: panic called with nil
  argument"` with the standard fallback body and status. **Held.** (See
  note N1 for the residual GODEBUG nuance.)
- **Reclassification on the global path with a 401 mapping.** The new
  tests pin the renderer path; this probe ran `RegisterStatusCode
  (ErrCodeInternal, 401)` plus `SetDefaultAuthenticateChallenge` against
  an auth error carrying its own challenge and an unencodable detail.
  Both JSON and problem fallbacks: status 401, the *default* challenge
  on the wire, the error's own challenge absent, and the problem body's
  `status` member agreeing at 401. **Held.**
- **Hijacked log variant through `Renderer.Middleware`.** The new
  regression tests exercise package-level `RecoveryMiddleware`; the
  renderer path shares `recoveryMiddleware`, and the probe confirmed the
  identical hijacked message, `hijacked=true`, absent `http_status`, and
  the `http.ErrAbortHandler` re-panic. **Held.**

## Notes, not findings

- **N1: panicnil GODEBUG.** The panicnil default follows the *main*
  module's go directive, not this package's — a consumer whose own
  go.mod predates 1.21 gets legacy semantics, under which a
  `panic(nil)` marshaler would slip past both `safeJSONMarshal` and
  `encoding/json`'s own recovery (the identical pre-existing behavior
  without svcerr in the path — the stdlib encoder's `recover() != nil`
  check has the same blind spot). Not a regression, not reachable
  except by a marshaler that deliberately panics with literal nil, and
  already the documented GODEBUG caveat pattern `recoveryMiddleware`'s
  comment establishes. Nothing to do beyond this record.
- **N2: hijacked records changed log shape.** A log pipeline that
  requires `http_status` on every recovery record will now see it
  absent on hijacked-panic records (previously a meaningless 0). This
  is the intended contract — no HTTP status applies — and the message
  variant makes the records self-describing, but it is technically an
  observable change to log output within a patch release. The tag
  message discloses it; acceptable.

## Open items, unchanged

- The v1.0.0 constructor narrowing (value-less `WrapValidationError`,
  query-less `NewDatabaseError`) stays open by design, awaiting real
  consumer demand for additive v1.x constructors.
- `zerologadapter`'s `any` spelling is committed but unreleased; it
  rides with the adapter's next substantive tag. No action needed.

## Verification performed

At `4566a33` (tag `v1.0.1` = `f5667c6`; the delta is one docs file),
Go 1.26.5, darwin/arm64:

- `go test -count=1 -cover ./...` — **root 100.0%, adapter 100.0%**
- `go test -race -count=1 ./...` — clean, both modules
- `go test -shuffle=on -count=5 ./...` — clean
- `go vet ./...`, `gofmt -l .` — clean, both modules
- `golangci-lint run` — 0 issues
- `GOWORK=off go mod tidy -diff` — no drift
- CI green on all four matrix lanes at both `f5667c6` and `4566a33`
  (Linux Go 1.21 floor included)
- `GOPROXY=https://proxy.golang.org go list -m
  github.com/n-ae/svcerr@v1.0.1` — resolves
- Three falsification probes (removed afterward), all held

## Revised rating

| Area | v1.0.0 | v1.0.1 |
|---|---|---|
| Problem selection | 9/10 | 9/10 |
| Scope and usability | 9.8/10 | 9.8/10 |
| Go idioms | 9.8/10 | 9.8/10 |
| Extensibility | 9.7/10 | 9.7/10 |
| Response safety | 9.7/10 | 9.8/10 |
| Middleware transparency | 9.7/10 | 9.8/10 |
| Production readiness | 9.8/10 | 9.9/10 |

Response safety recovers its rc-era mark: the two uncounted defects are
closed with structural fixes (a removed parameter, a shared guard)
rather than patched call sites, which is the durable kind of closure.
Middleware transparency ticks up for the honest hijacked log record.
The series has no open finding against this repository; the next
assessment should be event-driven — a real consumer report, a
substantive feature, or a toolchain floor change — rather than
calendar-driven.
