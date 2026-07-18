---
adr: 0001
title: Logger has no context.Context parameter
date: 2026-07-18
status: Accepted
---

# ADR 0001: Logger has no context.Context parameter

## Status

Accepted.

## Context

`Logger.Log(level Level, err error, fields map[string]any, msg string)`
(`logger.go:22`) is the only method a caller implements to plug their
logging library into `WriteHTTPError`, `WriteHTTPErrorHTML`,
`WriteHTTPProblem`, and `RecoveryMiddleware`. It takes no
`context.Context`, unlike `log/slog`'s context-aware methods
(`InfoContext`, `ErrorContext`, ...), so a caller whose logger extracts
trace IDs, span data, or other request-scoped attributes from a context
can't do that at this package's log call sites - only by wrapping the
result of `WriteJSONResult`/`WriteHTTPError` themselves and re-logging
with their own context, which makes the renderer-integrated logging
this interface exists for redundant.

This has been flagged three times without changing: the v1.0.1+HEAD
cross-review (`assessment-maintainable-architect-v4-head-2026-07-18.md`)
logged it as "Deferred: context-aware logging"; the v1.0.2 review
(`assessment-maintainable-architect-v4-v1.0.2.md`, claim 4) rediscovered
the same open item and confirmed no code had changed. Each time the
disposition was the same: sensible, additive, but not worth the API
surface until a real caller needs it.

## Decision

Keep `Logger.Log` context-free. Do not add a `ContextLogger` interface,
`Renderer.JSONContext`, or any other context-aware overload speculatively.

This package's own logging call sites (`safeLog` in `logging.go:123`,
called from `logError` and `RecoveryMiddleware`) have no `context.Context`
available to forward in the first place unless it's threaded through
every public `Write*` function's signature - a breaking change to the
package's core entry points, not an additive one. A caller who needs
context-derived fields today can already get them without that: call
`WriteJSONResult`/`WriteHTTPError` for the response, and separately log
through their own context-aware logger call, keyed by the same error and
the `WriteResult` it returns. That's more code at the call site than an
integrated `LogContext` would be, but it costs nothing in the package's
API surface for callers who don't need it, and this package has no
consumer report asking for the integrated version - only review findings
noting its absence in the abstract.

## Consequences

- No breaking change, no new exported surface, no maintenance cost until
  this is revisited.
- A caller who wants request-scoped fields in the log record produced by
  this package's own renderers cannot get them without duplicating the
  log call outside the package. This is a real ergonomic gap, accepted as
  the cost of not carrying speculative API surface.
- Revisit only on a real trigger: a consumer asking for this, or a
  substantive feature/refactor already touching every `Write*` signature
  for an unrelated reason. Do not revisit on a calendar or because a
  future review re-notices the same absence - the next assessment that
  raises this without a new consumer signal should link here rather than
  re-litigate it as a fresh finding.
