# Maintainable Architect Assessment — v4
**Project:** svcerr (github.com/n-ae/svcerr)
**Version reviewed:** v0.6.4 (`07c91b0`, `v0.6.4-2-g07c91b0`; the two commits past the tag only bump the adapter requirement and add assessment 0009)
**Date:** 2026-07-17
**Reviewer:** maintainable-architect-v4
**Prior assessment (this series):** [docs/assessment-maintainable-architect-v4-v0.5.0.md](assessment-maintainable-architect-v4-v0.5.0.md) (v0.5.0, 2026-07-16)
**Cross-examined inputs:** [docs/repository-assessment-v0.6.4-codex-2026-07-17.md](repository-assessment-v0.6.4-codex-2026-07-17.md) (Codex direct review) and an independent external v0.6.4 review supplied alongside it ("second opinion" below)

---

## Verdict

v0.6.4 is production-ready. I found no critical or high-severity defect, and
every response-safety problem this series raised at v0.5.0 is now fixed or
explicitly documented as a design decision. The release's engineering
discipline is genuinely strong: 99.8% root-module statement coverage, 100%
adapter coverage, clean race and shuffle runs, a dependency-free root module,
and unusually careful handling of `net/http`'s commit semantics.

I independently reproduced every disputed behavioral claim made by the two
input reviews with focused temporary tests (removed afterward). The two
reviews agree with each other and with my own reading far more than they
disagree; consolidated, the remaining issues are:

| Severity | Count | Summary |
|---|---:|---|
| Critical / high | 0 | — |
| Medium | 2 | Post-construction mutation bypasses wire invariants (`RetryAfter`); `PublicDetails` leaks internal mutable maps |
| Low | 5 | `errors.Join` order-dependence undocumented; commit-then-panic tracking gap; log-field gaps for three types; RFC 9457 reserved-member collision; two stale doc pointers |

None of these blocks adoption. All of them are post-construction-mutation or
documentation issues, not flaws in the happy-path response pipeline.

### What v0.6.4 closed from this series' v0.5.0 findings

| v0.5.0 finding | v0.6.4 |
|---|---|
| `ResponseController.Flush()` could lose `FlushError` | Fixed — `flushErrorTracker`/`flushErrorHijackTracker` preserve `FlushError() error` with per-layer priority matching `ResponseController` |
| Recovery could retain stale `Content-Encoding` | Fixed — `prepareErrorHeaders` deletes it (plus `Content-Length`, `Trailer`, `Retry-After`, `WWW-Authenticate`) |
| 401 responses omitted `WWW-Authenticate` | Partially — opt-in `SetAuthenticateChallenge` exists; no application-wide default (see "Design decisions") |
| Serialization/write failures invisible | Fixed — `WriteResult` plus `response_render_error`/`response_write_error`/`rendered_error_code` log fields |
| Wrapper dropped optional interfaces | Documented — `http.Pusher`/`io.ReaderFrom` still dropped, now stated in `newTrackingResponseWriter`'s doc |
| README overstated constructor symmetry | Fixed — `WrapAuthenticationError`, `WrapNotFoundError`, `WrapConflictError`, `WrapRateLimitError` all exist |

Beyond that, v0.6.4's own changelog claims all verified in source and tests:
nested `Unwrap` capability discovery (`discoverFlusher`/`discoverHijacker`),
delegate-before-record `WriteHeader` (invalid-status panics recoverable —
reproduced), HTML `Retry-After` (`writeHTMLErrorBody` calls
`rateLimitRetryAfterHeader`), short-write detection (`checkedWrite` →
`io.ErrShortWrite`), and single-node classification for logs
(`errorLogFields` uses `outermostCoded`).

---

## Cross-examination of the two input reviews

Both reviews are accurate on every claim I tested. Consolidated:

| Claim | Codex | Second opinion | My verification |
|---|---|---|---|
| Mutated `RetryAfter` reaches the wire unclamped | M1 | §3 | **Reproduced** — `Retry-After: -9` on JSON, problem+json, *and* HTML; JSON details also `-9`; `Context()` still shows the constructor value |
| `PublicDetails` returns internal maps by reference | — (missed) | §6 | **Reproduced** — mutating the returned map injects keys into the response, bypassing `SetPublicDetail` |
| `errors.Join` classification is child-order-dependent | — (missed) | §1 | **Reproduced** — `Join(notFound, internal)` → 404, `Join(internal, notFound)` → 500 |
| Bare 401 without `WWW-Authenticate` | — (noted as design) | §2 | **Reproduced** — deliberate and documented on `Authenticator` |
| Commit-then-panic `WriteHeader` confuses tracking | — (missed) | §4 | **Reproduced with a correction** — see L2 below; the failure mode depends on whether the broken writer panics once or always |
| Log fields missing for Conflict/RateLimit/Internal | L1 | — (missed) | **Reproduced** — `resource_type`/`conflict_key`, `service`/`limit`/`retry_after`, `component` all absent |
| Extensions can occupy RFC 9457 `instance`/`detail` | L2 | — (missed) | **Reproduced** — `SetPublicDetail("instance", 123)` emits `"instance":123` |
| Two stale doc pointers | L3 | — | **Confirmed** — README's `errors.go` pointer; `defaultMessageForCode`'s title claim |
| Fixed header/compression policy | — (noted as design) | §5 | Confirmed as documented design decision |

The reviews are complementary: Codex found the logging/RFC 9457/doc issues
the second opinion missed; the second opinion found the `errors.Join`,
`PublicDetails`, and commit-then-panic issues Codex missed. Codex's framing
of M1 as *duplicated mutable state* (private constructor snapshot vs. public
writable fields) is the better root-cause analysis — the negative
`Retry-After` is one symptom of it, not the whole problem.

One factual refinement to the second opinion's §4: with a writer that
*always* panics in `WriteHeader`, recovery's replacement write panics again
and escapes `RecoveryMiddleware` entirely (net/http's per-connection recovery
catches it; no corrupt body is sent, but the structured log for the second
failure is lost). The corrupt-body scenario it describes requires a writer
that panics *once* and then behaves — plausible for a nil-map-style bug, so
the finding stands, but the reproduction needs that refinement.

---

## M1. Post-construction mutation bypasses construction-time invariants

The strongest concrete symptom: `clampRetryAfter` floors the constructor
argument at zero, but `RateLimitError.RetryAfter` is an exported writable
`int` and all three renderers format its *current* value:

```go
err := svcerr.NewRateLimitError("api", 100, 30)
err.RetryAfter = -9
svcerr.WriteJSON(w, err)   // Retry-After: -9, details.retry_after: -9
```

RFC 9110 §10.2.3 defines `delay-seconds` as a non-negative decimal integer,
so `-9` is an invalid header value on the wire. Worse, `clampRetryAfter`'s
own doc comment claims the construction-time clamp "keeps the stored
RetryAfter field and the 'retry_after' context entry consistent with what a
caller actually sees on the wire" — a claim that ordinary field assignment
silently falsifies. The `Context()` snapshot (still `30`) then disagrees with
both the header and the JSON details (`-9`), so logs and responses describe
different events.

Related, unclamped by design: `ExternalAPIError.RetryAfter *int` is
*documented* for direct post-construction assignment (`errors.go:577`) and is
never clamped anywhere — a negative value flows into `details.retry_after`.
It never becomes a header, which is itself a small inconsistency: a
rate-limit `RetryAfter` becomes a `Retry-After` header, an external-API one
never does, even on the 502 response.

### Recommended fix

1. Re-clamp at the wire boundary — `rateLimitRetryAfterHeader` and the
   `extractErrorDetails` case — with a regression test that mutates the field
   after construction. This is a five-line, non-breaking change and closes
   the RFC violation regardless of the longer-term mutability decision.
2. Fix `clampRetryAfter`'s comment to stop claiming an invariant it doesn't
   enforce.
3. For a future breaking release, decide whether semantic errors are
   immutable values or mutable objects (Codex's recommendation, which I
   endorse). The current halfway state — private snapshot plus writable
   public fields, with one field (`ExternalAPIError.RetryAfter`) explicitly
   inviting mutation — is the root cause of this entire finding class.

## M2. `PublicDetails` returns the internal maps by reference

`BaseError.PublicDetails()` (`errors.go:280-282`) returns
`publicDetailAdditions` and `publicDetailRemovals` directly. The caller can
then mutate error state without going through the setters:

```go
add, _ := err.PublicDetails()
add["secret"] = "unexpectedly public"   // appears in the next response
```

Reproduced: the injected key reaches the JSON body. This bypasses the
last-call-wins bookkeeping `SetPublicDetail`/`RemovePublicDetail` maintain
(direct map writes don't clear the opposing map), creates an unadvertised
mutation path the concurrency documentation doesn't cover, and contrasts
with `Context()`, which deliberately returns a shallow copy and documents
exactly what that does and doesn't protect.

### Recommended fix

Return copies from `BaseError.PublicDetails`, mirroring `Context()`'s
approach and doc style. The module floor is Go 1.20, so `maps.Clone`
(Go 1.21) isn't available — a small manual copy helper is needed. The
`PublicDetailer` interface contract doesn't change; `extractErrorDetails`
already copies entries into its own map, so the only behavioral change is
closing the escape. Cost: two small allocations per rendered error that uses
public details — negligible on an error path.

## L1. `errors.Join` classification is child-order-dependent and undocumented

`outermostCoded` uses `errors.As`, which traverses error trees pre-order,
depth-first. For a joined error the first coded child wins:

```go
GetErrorCode(errors.Join(notFound, internal))  // NOT_FOUND  → 404
GetErrorCode(errors.Join(internal, notFound))  // INTERNAL_ERROR → 500
```

Reproduced exactly as the second opinion states. Reversing the arguments of
`errors.Join` flips the client-visible status between 404 and 500 — easy to
trip in a handler that joins a domain error with a cleanup error. This is
inherent `errors.As` semantics, not a svcerr bug, and because status,
message, details, headers, and log fields all derive from the same
`outermostCoded` node, the response at least stays *internally* consistent
whichever child wins.

### Recommended fix

Document it (package doc + README): joined errors classify by the first
coded error in traversal order, and callers aggregating a client-facing
error with an operational one should explicitly re-classify
(`svcerr.Wrap(joined, svcerr.ErrCodeInternal, ...)`). I would *not* adopt
the second opinion's alternative of a severity-priority traversal (prefer
5xx over 4xx): it requires walking the full tree with bespoke traversal
code, diverges from stdlib `errors.As` expectations, and changes behavior
for existing single-chain users in edge cases. Documentation plus the
explicit-wrap idiom is the right cost/benefit.

## L2. Commitment tracking misses a delegated `WriteHeader` that commits, then panics

v0.6.4 deliberately delegates before recording commitment so that a real
writer's invalid-status panic (which commits nothing) stays recoverable —
verified working. The mirror-image gap: an intermediate writer *between*
`RecoveryMiddleware` and the transport whose `WriteHeader` commits downstream
and then panics. The delegate call never returns, `wroteHeader` stays false,
and recovery believes the response is uncommitted.

Reproduced, with the failure mode depending on the broken writer:

- Panics **once** (e.g. a nil-map bug in metrics code): recovery writes its
  500 JSON document onto the already-committed 200 — the client sees status
  200 with an `INTERNAL_ERROR` body appended. This is the corruption the
  second opinion describes.
- Panics **always**: recovery's own replacement write re-panics; the second
  panic escapes to net/http's per-connection recovery. No corrupt body, but
  svcerr's structured log for the second failure is lost.

Note the existing asymmetry inside `trackingResponseWriter`: `Write` records
commitment *before* delegating; `WriteHeader` records it *after*. Each
direction is individually justified, but the pair means the tracker is
conservative against double-writes on the body path and optimistic on the
header path.

### Recommended fix

The second opinion's suggestion — validate `100 <= status <= 999` in the
tracker itself (matching net/http's `checkWriteHeaderCode` range), then
record commitment *before* delegating — closes both edges: invalid statuses
still panic pre-commitment and stay recoverable, and a valid delegated call
is conservatively assumed to have committed. The tradeoff is real and should
be stated in the comment: if a downstream writer panics on a *valid* status
without committing anything, recovery now aborts the connection instead of
sending a clean 500. Aborting a connection is strictly safer than corrupting
a committed response, so the trade is sound. Severity stays low because the
recommended deployment (recovery outermost, wrapping the server's own
writer) is immune.

## L3. Structured logging omits type-specific fields for three built-in types

`errorLogFields` (`http.go:706-719`) covers Validation, Database,
ExternalAPI, Authentication, and NotFound, but not:

- `ConflictError` — no `resource_type`/`conflict_key`;
- `RateLimitError` — no `service`/`limit`/`retry_after`;
- `InternalError` — no `component`.

All reproduced. The `InternalError` gap bites hardest: a 5xx logs a stack
trace but not which component failed, the one field its constructor exists
to carry. Codex's maintenance observation is correct — adding a built-in
type touches constructors, detail extraction, message policy, status
mapping, and this hand-written switch, and 99.8% line coverage doesn't prove
those projections are *complete*. Add the three cases plus one table-driven
completeness test asserting every built-in type's safe fields appear (and
that sensitive context — SQL text, URLs, validation values — does not).

## L4. Public details can occupy reserved RFC 9457 members

`ProblemDetails.MarshalJSON` copies `Extensions` first, then overwrites
`type`/`title`/`status`/`code` unconditionally — but writes `detail` and
`instance` only when non-empty. Reproduced:
`SetPublicDetail("instance", 123)` emits `"instance":123`, a registered
member with the wrong JSON type that RFC 9457 §3.1 obliges consumers to
ignore. The `detail` slot is reachable the same way only when the outermost
node's own message is empty — rarer, same fix. Reserve all six names
unconditionally when copying extensions; add collision tests for each.

## L5. Two documentation pointers have drifted

Confirmed both Codex L3 items:

1. `README.md:190-192` points to the package doc comment in `errors.go` for
   "the full list of codes and their HTTP status mapping"; that comment lists
   the semantic types, and the mapping actually lives in `HTTPStatusCode`.
2. `defaultMessageForCode`'s comment (`http.go:569-573`) still says it's used
   as the RFC 9457 title; `writeProblemJSONBody` correctly uses
   `http.StatusText` plus optional `ProblemTitler` instead.

Add to the same bucket: `clampRetryAfter`'s consistency claim (see M1).

---

## Design decisions, not defects

These recur across reviews and should stay on the roadmap, but each is
deliberate, documented in source or README, and defensible:

- **Bare 401s.** `SetAuthenticateChallenge` is opt-in per error; an
  `AuthenticationError` without it yields a 401 with no `WWW-Authenticate`,
  which RFC 9110 §11.6.1 requires. The package's position — it cannot invent
  an application's scheme/realm — is documented on `Authenticator`. The
  second opinion's `Renderer`-with-default proposal is the right eventual
  shape, but svcerr's API is package-level functions; an application-wide
  default means either a config struct/renderer type (a significant API
  addition) or package-level mutable state (like `RegisterStatusCode`, e.g.
  a `SetDefaultAuthenticateChallenge`). The latter is the cheap, consistent
  interim step; precedence error-specific → application default is obvious.
- **Fixed header policy.** All writers delete `Content-Encoding`; `ETag`,
  `Last-Modified`, `Accept-Ranges` are retained. Both choices are documented
  with reasoning in `prepareErrorHeaders`, and the compression
  middleware-ordering constraint is in the README. A configurable
  `HeaderPolicy` (with different defaults for normal errors vs.
  panic-replacement) is a reasonable v0.7 feature, not a v0.6.4 defect.
- **No `BytesWritten` in `WriteResult`.** Nice-to-have for callers doing
  their own accounting; nothing currently misleads.

---

## Test assessment

Verified locally on the clean `main` worktree at `07c91b0` with Go 1.26.5:

- `go test -count=1 ./...` — pass (both modules)
- `go test -race -count=1 ./...` — pass
- `go test -shuffle=on -count=2 ./...` — pass
- `go vet ./...` — clean (both modules)
- `gofmt -l .` — no output
- `GOWORK=off go mod tidy -diff` — no diff
- Root coverage 99.8%, adapter coverage 100.0%

Focused temporary probes (one external-package test file, deleted after the
run) reproduced: join-order classification flip, negative `Retry-After` on
all three renderers, `PublicDetails` map escape, the three log-field gaps,
the RFC 9457 `instance` collision, the bare 401, and the commit-then-panic
tracking gap in both its once-panicking and always-panicking variants — and
confirmed the invalid-`WriteHeader` recovery fix still works.

Not independently rerun: the Go 1.20/1.25 floor toolchains, proxy
consumption of published tags, vulnerability scanning, benchmarks. CI covers
the floors; assessment 0009 records tag/proxy verification.

I second Codex's two structural test observations: every root test is
internal (`package svcerr`), so nothing continuously exercises the package
exactly as a consumer sees it — a small `package svcerr_test` contract suite
plus compiled `Example` functions would close that; and CI should add a
stable-toolchain `-race` lane (local race runs are clean, but the global
`RegisterStatusCode` registry deserves continuous protection).

---

## Revised rating

| Area | v0.5.0 (this series) | v0.6.4 |
|---|---|---|
| Problem selection | 9/10 | 9/10 |
| Scope and usability | 9.3/10 | 9.5/10 |
| Go idioms | 8.8/10 | 9.3/10 |
| Extensibility | 8.8/10 | 9.2/10 |
| Response safety | 8.7/10 | 9.6/10 |
| Middleware transparency | 8.5/10 | 9.4/10 |
| Production readiness | 8.8/10 | 9.5/10 |

The jump in response safety and transparency is earned: every concrete
wire-corruption path this series identified at v0.5.0 is closed, and the
remaining findings all require post-construction mutation, unusual writer
stacks, or reserved-name collisions to trigger.

---

## Priority for v0.6.5

1. Clamp `RetryAfter` at emission (header **and** details), fix
   `clampRetryAfter`'s comment, add a mutation regression test (M1).
2. Return copies from `BaseError.PublicDetails` with a Go 1.20-compatible
   helper (M2).
3. Document `errors.Join` classification order and the explicit-wrap idiom
   (L1).
4. Add the three missing log-field cases plus a completeness table test (L3).
5. Reserve RFC 9457 member names when flattening extensions (L4).
6. Fix the three stale doc pointers (L5).
7. Harden `WriteHeader` tracking: validate 100–999, record commitment before
   delegating, document the abort-vs-corrupt tradeoff (L2).

For v0.7.x: an application-wide default `WWW-Authenticate` mechanism, a
configurable header policy, an external contract-test suite with compiled
examples, and a CI race lane. Before v1: resolve the mutable-object vs.
immutable-value question for semantic errors — it is the root cause behind
M1, M2, and half of this file's predecessor findings.

v0.6.4 is a safe production choice today, including for streaming handlers
and custom writer stacks, provided recovery wraps the server's writer
directly and teams treat constructed errors as immutable.
