---
adr: 0002
title: A marshal-panic log record keeps the original error's stack, not a second one
date: 2026-07-18
status: Accepted
---

# ADR 0002: A marshal-panic log record keeps the original error's stack, not a second one

## Status

Accepted.

## Context

`safeJSONMarshal` (`render.go:153-170`) recovers a panicking
`json.Marshaler` - most often a caller's custom `MarshalJSON` on a value
passed to `SetPublicDetail` - and turns it into an error, preserving
identity via `%w` when the panic value was itself an error
(`render.go:158`). `writeJSONErrorBody`/`writeProblemJSONBody` then fall
back to a generic body and pass that error through as `renderErr`.

`errorLogFields` (`logging.go:7-65`) builds the log record's fields from
the *original application error* regardless of whether rendering it
later panicked: `stack_trace` comes from `GetStackTrace(err)`
(`logging.go:25-27`), captured at that error's own construction site
(e.g. wherever `NewValidationError` was called), not from the
marshaler's panic site. `logError` (`logging.go:79-100`) separately adds
`response_render_error` with the panic's text (`logging.go:89`), with no
stack of its own alongside it.

The v1.0.2 review (`assessment-maintainable-architect-v4-v1.0.2.md`,
note N1) confirmed this as-described: a response log describing a
marshal panic shows the stack from an earlier `NewValidationError` call,
not the marshaler's panic site. It proposed a private
`marshalPanicError` type capturing its own PC stack in
`safeJSONMarshal`'s deferred recover, surfaced as a separate field (e.g.
`response_render_stack`), and declined to implement it - for the same
reason the v1.0.1 cycle declined a stack-capturing `RenderErr` wrapper
one cycle earlier: the field is accurate for what it answers, and the
marshal panic's own site adds no information a stack trace is needed
for.

## Decision

Do not capture a second stack trace at the `safeJSONMarshal` recover
site. `stack_trace` continues to name the original application error's
construction site; `response_render_error` continues to carry the panic
text alone.

The two facts a 2am reader needs from this log record are "what error was
being reported" (answered by `stack_trace`, pointing at where that error
was constructed) and "why didn't the client get it" (answered by
`response_render_error`'s text, e.g. `svcerr: JSON marshaler panicked:
...`). A panicking `MarshalJSON` is a caller-supplied method with no
call-time variance - it panics because of what it does, not where it was
called from - so a runtime-captured stack pointing into it says no more
than the panic message already does, and less than reading that one
function's source does. Capturing it anyway would mean carrying a second
PC-capture path, a second exported-or-not stack-formatting concern, and a
second field for every consumer of this package's logs to decide whether
to display, for a fact that's fully recoverable by hand from the panic
text and the marshaler's own source.

## Consequences

- The log record's `stack_trace` field always means "where the
  logged error was constructed," even on a render failure - one
  consistent meaning across every log record this package produces, not
  a field whose meaning changes depending on whether rendering also
  failed.
- Diagnosing exactly which line inside a custom `MarshalJSON` panicked
  still requires reading that method's source, informed by
  `response_render_error`'s panic text - no runtime stack shortcut is
  provided for it.
- Revisit only if a real incident shows the panic text plus the
  marshaler's source insufficient to diagnose an actual production
  failure - not because a future review re-notices the same field
  provenance in the abstract. The next assessment that raises this
  should link here rather than re-litigate it as a fresh finding.
