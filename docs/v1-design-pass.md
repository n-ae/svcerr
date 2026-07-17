---
title: Pre-v1 design pass — svcerr
date: 2026-07-17
status: Draft for maintainer review
author: maintainable-architect-v4 (design mode)
inputs:
  - docs/assessment-maintainable-architect-v4-v0.6.4.md
  - docs/repository-assessment-v0.6.4-codex-2026-07-17.md
  - docs/assessment-maintainable-architect-v4-v0.9.0.md
baseline: v0.10.0 (9aaed44)
---

# Pre-v1 design pass

Every assessment since v0.6.4 has deferred the same three structural
questions to "one design pass before v1", on the grounds that they
interact and shouldn't be decided piecemeal. This document is that pass:
it states each decision, the options considered, a recommendation, and a
staged migration plan that reaches v1.0.0 without a big-bang rewrite.

The three decisions:

1. **Mutability**: are semantic errors immutable values or mutable
   objects? (Root cause behind findings M1/M2 and every emission-side
   clamp.)
2. **Configuration**: do the accumulating package-level globals
   consolidate into an instance-level `Renderer`?
3. **Structure**: how does the 1,400-line `http.go` split by
   responsibility, and when?

They interact: a `Renderer` is the natural home for per-instance (rather
than global) policy; immutable errors remove the need for emission-side
clamping that the renderer currently performs; and the file split is
easiest *before* the API work, so the new surfaces land in the right
files instead of deepening the hotspot.

Current-state inventory (v0.10.0): 37 exported package-level functions,
18 exported writable identity fields across the 8 semantic types, 4
mutex-guarded global config surfaces (status registry, default
challenge, two header-policy slots), `http.go` at ~1,430 lines.

---

## Decision 1: Immutable values vs. mutable objects

### The problem being solved

Each semantic error stores the same facts twice: `BaseError` privately
snapshots `code`, `message`, and a `context` map at construction, while
the concrete type exposes the same facts as exported writable fields
(`RateLimitError.RetryAfter`, `AuthenticationError.Reason`,
`NotFoundError.ResourceID`, ...). Nothing keeps them synchronized, so a
field assignment after construction desyncs classification from
presentation. The concrete v0.6.x bugs (negative `Retry-After` on the
wire, log fields describing a different reason than the code) were all
instances of this, and the v0.6.5 emission clamps are compensations, not
a fix. One field (`ExternalAPIError.RetryAfter`) is even *documented*
for post-construction assignment.

Meanwhile, the `Set*` presentation setters (`SetPublicMessage`,
`SetPublicDetail`, `SetProblemType`, `SetAuthenticateChallenge`, ...)
have caused no such bugs: each is the single source of truth for its
concern, and the construct-configure-return idiom they enable is used
throughout the README and by consumers.

### Options

**A. Fully immutable values.** Private fields everywhere; all
configuration via constructor options or `With*` copy-returning methods.
Strongest invariants, but it removes the construct-configure-return
idiom the package's documentation is built around, turns 10 cheap
setters into copy-returning methods on 8 concrete types (embedded-struct
copying subtleties included), and rewrites every consumer call site -
cost far beyond what the observed failure class justifies.

**B. Immutable identity, mutable presentation.** Unexport the 18
identity fields (add getters); keep the existing `Set*` presentation
setters as-is. Identity - the facts that drive code, status, message,
details extraction, headers, and log fields - becomes fixed at
construction; presentation remains configurable before the error is
returned. `ExternalAPIError`'s retry hint, the one identity-adjacent
value legitimately learned after construction, becomes an explicit
`SetRetryAfter(seconds int)` that clamps - making it a sanctioned,
invariant-preserving mutation like the other setters.

**C. Status quo plus documentation.** Rejected: three assessments'
findings trace to it, and the emission clamps it necessitates are
exactly the kind of scattered defensive code a v1 should not enshrine.

### Recommendation: B

B fixes the entire observed failure class with the smallest idiom
change. Consequences worth naming:

- `errors.As(err, &nfErr); nfErr.ResourceID` becomes
  `nfErr.ResourceID()` - the only change most consumers will see.
- The `context` map duplication **disappears**: `Context()` derives from
  the canonical private fields on demand instead of snapshotting at
  construction. One state, projected. (This also retires the shallow-copy
  caveat for identity keys.)
- The emission clamps added in v0.6.5 (`retryAfterHeader`,
  `extractErrorDetails`, `errorLogFields`) become dead weight and are
  removed - the constructor/`SetRetryAfter` clamp makes the invariant
  real. The regression tests stay, now proving the invariant instead of
  the compensation.
- Code-deriving fields (`Reason`, `Operation`) can no longer desync from
  the code derived from them.
- The concurrency documentation simplifies: identity is safe to read
  concurrently once constructed; only the documented `Set*` calls remain
  construction-time-only.

## Decision 2: Consolidate configuration into a Renderer

### The problem being solved

Four global config surfaces have accreted, each with its own mutex,
setter, and getter idiom. Each was individually right (and they are
mutually consistent), but the pattern scales linearly in globals, makes
two differently-configured services in one process impossible, and
forces every test and example touching config into cleanup choreography.
The write API has also grown to a 3×3 matrix (`WriteHTTPError` /
`WriteJSON` / `WriteJSONResult` × JSON/HTML/problem) whose only
difference is logging and result reporting.

### Options

**A. Keep globals.** Simple, but v1 would freeze the accretion pattern
as the permanent API.

**B. Instance `Renderer`, package-level default.**

```go
type Renderer struct{ /* private */ }

func NewRenderer(cfg RendererConfig) *Renderer

type RendererConfig struct {
	StatusCodes                  map[ErrorCode]int // merged over built-ins
	DefaultAuthenticateChallenge string
	HeaderPolicy                 HeaderPolicy
	RecoveryHeaderPolicy         HeaderPolicy
	Logger                       Logger // used by write methods and recovery
}

func (r *Renderer) JSON(w http.ResponseWriter, err error) WriteResult
func (r *Renderer) HTML(w http.ResponseWriter, err error) WriteResult
func (r *Renderer) Problem(w http.ResponseWriter, err error) WriteResult
func (r *Renderer) Middleware() func(http.Handler) http.Handler
```

Each method logs through `cfg.Logger` when set *and* returns the
`WriteResult` - collapsing the 3×3 matrix to 3 methods plus recovery.
The existing package-level functions become thin delegates to a default
`Renderer`; the existing global setters mutate that default renderer's
config. Nothing breaks; the globals become the convenience layer instead
of the foundation.

**C. A config struct but still global-only.** Consolidates the setters
but keeps the one-config-per-process limit; halfway for little saving
over B.

### Recommendation: B

The `Renderer` is what both external reviews independently sketched, and
it resolves testability, multi-tenancy, and accretion in one move. Two
deliberate scope limits:

- **Package-level functions stay in v1.** `svcerr.WriteJSON(w, err)` is
  the package's front door and most consumers never need a second
  configuration; they delegate to the default renderer. The
  `WriteHTTPError`-with-logger triple and the `*Result` triple are
  *deprecated* in v1 (not removed) in favor of renderer methods - removal
  is a v2 question.
- **Renderer config is fixed at construction** (no setters on
  `Renderer`) - consistent with Decision 1's philosophy: instance
  identity immutable, the mutable global default remains the
  startup-configuration escape hatch.

## Decision 3: Split http.go by responsibility

Mechanical and uncontroversial - every assessment has recommended it;
the only question was sequencing. Proposed layout, no exported-API
change:

| File | Contents (today's names) |
|---|---|
| `status.go` | code→status mapping, `RegisterStatusCode`, registry |
| `render.go` | the three body writers, fallback bodies, `WriteResult`, message policy, details extraction |
| `headers.go` | `HeaderPolicy` + slots, `prepareErrorHeaders`, `retryAfterHeader`, challenge default + `setAuthenticateChallenge` |
| `logging.go` | `errorLogFields`, `logError`, `safeLog` (with `logger.go`) |
| `tracking.go` | `trackingResponseWriter` + capability trackers + discovery |
| `recovery.go` | `RecoveryMiddleware` |

Plus the finalization refactor the Codex review proposed: the three body
writers repeat classify → render → reset headers → restore
classification headers → write → report; extract one private
finalization helper so the sequence exists once (the HTML `Retry-After`
omission fixed in v0.6.4 was exactly this sequence drifting between
copies). Both are wire-behavior-preserving and fully covered by the
existing suite plus the external contract tests.

## Interactions and sequencing

The split must land first (new API goes into the right files), the
`Renderer` second (additive), immutability last (breaking). Cosmetic v1
items ride along: `interface{}` → `any` (36 occurrences; floor 1.20
permits it), and the Go floor itself should be re-evaluated (raising it
is breaking-adjacent; decide deliberately, not by accident).

## Staged migration plan

| Stage | Version | Breaking? | Contents |
|---|---|---|---|
| 0 | v0.11.0 | No | File split + finalization helper. Pure refactor; contract suite and wire-behavior tests must pass unchanged. |
| 1 | v0.12.0 | No | `Renderer`/`RendererConfig`; package-level functions delegate to a default renderer; global setters become views onto it. Contract tests for two coexisting renderers. |
| 2 | v0.13.0 | No | Getters added alongside the 18 exported fields; `ExternalAPIError.SetRetryAfter`; field docs marked "deprecated for v1: use the getter". Gives consumers a release to migrate mechanically. |
| 3 | v1.0.0 | Yes | Unexport identity fields; `Context()` derived, not snapshotted; emission clamps removed (tests retargeted at the constructor invariant); `WriteHTTP*`+`*Result` triples deprecated in docs; `interface{}` → `any`. Module path unchanged (v1 needs no suffix). |

Each stage is independently releasable and independently assessable -
the numbered-assessment cadence can gate each one.

## Open questions for the maintainer

1. **Getter naming.** `nfErr.ResourceID()` (bare, idiomatic) collides
   with nothing today - confirm no preference for `GetX` symmetry with
   `GetErrorCode`/`GetStackTrace` (which should themselves stay, as
   they're chain-walking helpers, not field getters).
2. **Logger placement.** Stage 1 puts the logger on `RendererConfig`;
   should the deprecated-in-docs `WriteHTTPError(w, err, logger)` triple
   be *removed* at v1.0.0 or only at a hypothetical v2? This document
   assumes kept-but-deprecated through v1.
3. **Go floor at v1.** Staying at 1.20 keeps the widest reach but rules
   out `maps.Clone`/`slices` internally; raising to 1.21+ is defensible
   at a major version. This document takes no position beyond "decide
   explicitly at stage 3".

## What this pass deliberately does not change

The classification model (`outermostCoded`, one node drives everything),
the capability-interface design (`Coder`, `PublicMessager`, ...), the
message-safety policy, the commit-tracking design, and the
zero-dependency root module are all working and validated by four
release cycles of assessment - v1 keeps them as-is.
