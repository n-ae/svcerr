---
assessment: 0006
title: svcerr v0.6.1 review
date: 2026-07-17
status: Accepted
reviewer: maintainable-architect-v4
prior: 0005-svcerr-v0.6.0-review.md (v0.6.0)
---

# Assessment 0006: svcerr v0.6.1 review

## Executive summary

v0.6.1 closes every finding from assessment 0005 (M1/M2/M5/M6 in 61d25a1, M3 in 8d4fc07, M4 in 3b546e9): the Go floor is verified-minimum rather than aspirational, CI now enforces it on a real floor/stable matrix, the auth-reason switch is deduplicated, and the documentation gaps are filled. `go vet`, `go test -count=1`, and `go test -race -count=1` pass clean on both modules (root and `zerologadapter`); coverage is 99.7% root / confirmed via `go tool cover`.

This assessment was prompted by an external review ("the verdict") claiming one critical recovery bug and several transport-layer issues. I reproduced all six of its claims directly against this tag using throwaway tests, and the reproductions mostly hold up — but the external review's causal analysis of its own headline finding is incomplete, and its proposed fix for that finding would not achieve what it thinks it achieves. That correction is the main content of this assessment; the rest of the external findings are confirmed with minor severity refinements.

### What actually reproduces

| # | External claim | Verdict |
|---|---|---|
| 1 | `panic(nil)` silently swallowed under Go 1.20 semantics | **Real, but mischaracterized** — see Major finding below. The proposed "cleanest fix" (raise `go.mod` to `go 1.21`) is technically effective but defeats the purpose of the Go-floor-lowering work this project just did in 8d4fc07. |
| 2 | Deleting `Content-Encoding` can corrupt responses behind compression middleware | **Real, and broader than described.** It is not a recovery/middleware-ordering issue — it reproduces on a plain `WriteJSON` call with no `RecoveryMiddleware` involved at all. |
| 3 | Response body write failures are invisible | **Real**, confirmed. |
| 4 | 401 responses have no default `WWW-Authenticate` | **Not a defect.** This is a deliberate, already-documented, already-tested trade-off (`SetAuthenticateChallenge` is opt-in because the package cannot invent an application's scheme). It's a reasonable future enhancement, not a finding. |
| 5 | Stale `Retry-After`/`WWW-Authenticate` can survive an unrelated response | **Real**, confirmed. |
| 6 | Negative `retryAfter` produces an invalid header | **Real**, confirmed. |

## Findings

### Critical

None.

### Major

**J1 — `RecoveryMiddleware` cannot distinguish `panic(nil)` from normal completion, and the "obvious" fix doesn't fix it for the audience that matters (S–M).**

`recover()` returns `nil` for `panic(nil)` unless the Go runtime is running with `panicnil=0` (the post-1.21 default). This isn't a property of svcerr's own `go.mod` — it's a property of **whichever module is the main module of the final build**, per Go's documented GODEBUG-versioning rule (`go help godebug` / `doc/godebug.md`, "Default GODEBUG Values"): only the work module's (or workspace's) `go` line is consulted; a dependency's own `go` line and any `godebug` block in it are ignored.

I verified this with three separate builds instead of trusting the mechanism from memory:

1. **svcerr as its own main module, `go 1.20`, no workspace** (`GOWORK=off`, matching a plain `git clone && go test`): `panic(nil)` recovers as `nil` → empty 200, zero log calls. Confirmed swallowed.
2. **svcerr imported by a consumer whose own `go.mod` says `go 1.21` or higher**: `panic(nil)` recovers as a non-nil `*runtime.PanicNilError` → correct 500 + log call, regardless of what svcerr's own `go.mod` says. svcerr's `go 1.20` directive has **no effect on this consumer's binary at all.**
3. **svcerr imported by a consumer whose own `go.mod` also says `go 1.20`** — i.e., exactly the population 8d4fc07 lowered the floor to keep serving: `panic(nil)` **is** swallowed in their production binary. This is the real exposure, and it's precisely the audience the recent floor-lowering work was meant to protect, not some unlikely edge case.

The external review's "cleanest fix" — bump svcerr's own `go.mod` to `go 1.21` — does work, but not the way it implies. It works only because Go's module graph pruning forces every consumer to also raise their own `go` line to at least 1.21 (I confirmed this too: `go mod tidy` on a `go 1.20` consumer importing a `go 1.21` svcerr rewrites the consumer's `go` line to `1.21` automatically, and a stale consumer `go.mod` fails the build outright with "updates to go.mod needed" until that happens). In other words: raising the floor "fixes" the bug by **evicting every Go-1.20 consumer from Go 1.20**, which is exactly the outcome 8d4fc07 was written to avoid. Recommending that fix without naming this tradeoff is a real gap in the external review, not a nuance.

The only fix that keeps Go 1.20 support *and* closes the bug is the code-level one the external review offers as its fallback option: track whether the handler returned normally, and treat a `nil` recovered value on an abnormal exit as a forced-panic (its `panic(nil)` / `runtime.Goexit` ambiguity is an acceptable cost — both should produce a 500, not a silent 200). Given the project's stated direction (support the floor, don't silently push people off it), this is the primary recommendation, not the "retain Go 1.20 support" fallback.

One more concrete consequence worth noting: this project's own CI floor lane (`go-version-file: go.mod`, i.e. an actual Go 1.20 toolchain) and its `stable` lane (no `go.work` in a clean checkout, so svcerr is the main module) are **both currently running in the vulnerable configuration** — and there is no test in the suite that would catch a regression here. Whatever fix lands, it needs a regression test exercising `panic(nil)` specifically (a plain `panic("x")` test doesn't exercise this path).

**J2 — Every error writer, not just `RecoveryMiddleware`, can silently swap a compressed body's `Content-Encoding` header for nothing (S).**

`prepareErrorHeaders` (http.go:200) unconditionally deletes `Content-Encoding` and is called by all three response writers — `writeJSONErrorBody` (http.go:158), `WriteHTTPErrorHTML` (http.go:259), and the problem+json writer (http.go:373) — not only from the recovery path. I reproduced this with a plain `WriteJSON(w, err)` call, no `RecoveryMiddleware` in the chain at all, against a `ResponseWriter` wrapper that sets `Content-Encoding: gzip` once at construction and transparently gzips every `Write`: the response header said no encoding, the body was gzip magic bytes.

The source comment at http.go:188-194 already reasons about this tradeoff, but frames it as safe when an outer compression layer "sets the header itself after compressing whatever's written" — i.e., it assumes headers are set lazily, on or after `Write`. Many real compression wrappers (including the common pattern of setting `Content-Encoding` once when the wrapper is constructed, before any `Write` call) don't meet that assumption, and nothing in the README states it as a hard requirement for callers — it's a code comment, not operator-facing documentation.

This also means the external review's proposed fix (split `prepareNormalErrorHeaders` from `prepareRecoveryHeaders`, only stripping `Content-Encoding` in the latter) doesn't address the actual reproduction, since the failure here has nothing to do with recovery. There is no clean universal fix available to the package itself — it cannot know whether the `Content-Encoding` on the `ResponseWriter` it was handed is stale (left over from a different response) or live (a transparent wrapper about to honor it). The realistic options are: (a) document plainly, at the README level, that svcerr's writers assume `Content-Encoding` reflects the plain-text body about to be written and are incompatible with wrappers that pre-set it before `Write`, or (b) accept a narrower default (don't touch `Content-Encoding` at all, document the original stale-gzip-on-panic-replacement risk as an accepted tradeoff instead — trading one documented gap for another). Either is legitimate; leaving it unstated is not.

### Minor

**J3 — Response body write failures are discarded (S).**

All three writers end with `w.WriteHeader(statusCode); _, _ = w.Write(body)` (http.go:174, 268, 384). `writeJSONErrorBody` already threads a `renderErr` (marshal failure) back to `logError` for exactly this kind of "the caller can't see what happened" reason — the plumbing for a second failure mode already exists in shape, it's just not populated from `w.Write`'s return value. Confirmed with a `Write` that always errors: `WriteHTTPError` returns normally and logs the original error code with no indication the body never reached the client. Extending the existing `(statusCode, renderErr)`-shaped return (rather than introducing a new public `WriteResult` type, as the external review suggests) is the smaller, more consistent fix given the code already has this shape.

**J4 — Stale `Retry-After`/`WWW-Authenticate` can survive onto an unrelated response (S).**

Confirmed: pre-setting `Retry-After: 999` and `WWW-Authenticate: Basic realm="old"` on the `ResponseWriter` before calling `WriteJSON` with a 404 leaves both headers on the final response, which qualifies for neither. `prepareErrorHeaders` already has an explicit, documented list of what it does and doesn't touch (Content-Length/Content-Encoding/Trailer cleared; ETag/Last-Modified/Accept-Ranges deliberately kept) — `Retry-After` and `WWW-Authenticate` fall into neither bucket today; they're just not mentioned. Worth resolving as a matter of the same reasoning already applied to the other conditional headers, not a new design question.

**J5 — `NewRateLimitError` accepts a negative `retryAfter` and emits it verbatim (S).**

Confirmed: `NewRateLimitError("service", 100, -1)` produces `Retry-After: -1`, not a valid `delay-seconds` value per RFC 9110 §10.2.3. Cheap to clamp at the constructor (`if retryAfter < 0 { retryAfter = 0 }`) or reject; no reason to propagate a value the writer will need to re-validate anyway.

## Accepted trade-offs (not findings)

- **401 responses have no default `WWW-Authenticate` challenge.** This is deliberate and already tested (`SetAuthenticateChallenge` exists precisely because the package can't invent a scheme/realm). A renderer-level `DefaultAuthenticateChallenge` remains a reasonable future ergonomics improvement — see recommendations — but its absence today is not a bug.
- All trade-offs restated in 0005 (Flusher/Hijacker-only preserved interfaces, opt-out structured details, shallow `Context()`, non-concurrent-safe error mutation, generic custom-code messages) still hold and remain documented.

## Recommendations

**This week:**
1. Add a regression test that panics with a literal `nil` through `RecoveryMiddleware` — the suite currently has no test that would catch J1 regressing either direction.
2. Fix `RecoveryMiddleware` to treat an abnormal exit with a `nil` recovered value as a forced internal error (track "returned normally" rather than trusting `recover() == nil`), per J1. This is the one fix on this list that changes runtime behavior for Go-1.20 consumers today.
3. Thread the `Write` error into the same result path `renderErr` already uses, and log it (J3).
4. Clamp or reject negative `retryAfter` in `NewRateLimitError` (J5).

**This month:**
5. Add one README paragraph stating the `Content-Encoding`-clearing assumption as a hard compatibility note for callers running svcerr's writers behind transparent-compression `ResponseWriter` wrappers (J2) — there's no code fix available, only a documentation one, so make sure it's the kind of documentation someone would find before shipping the combination, not just a source comment.
6. Decide and document whether `Retry-After`/`WWW-Authenticate` should be cleared alongside Content-Length/Trailer, or explicitly join the ETag/Last-Modified "left alone" list (J4).

**This quarter (optional):**
7. Evaluate a renderer-level `DefaultAuthenticateChallenge` config if real usage shows callers routinely forgetting `SetAuthenticateChallenge` — don't build it speculatively.

## Verification performed

Against this working tree (commit `3b546e9`, tag `v0.6.1`, no diff between them): `go vet ./...`, `go test -count=1 ./...`, `go test -race -count=1 ./...` on both the root module and `zerologadapter`, all clean. Coverage independently confirmed at 99.7% via `go tool cover -func`. All six external claims and the three-build GODEBUG/module-graph experiment for J1 were reproduced with throwaway tests, not asserted from documentation alone.
