# Architecture Decision Records

Decisions this project deliberately made and does not intend to
revisit absent a new, concrete trigger (a consumer report, an incident,
a change already touching the same surface for an unrelated reason).
An ADR here means "considered and declined/deferred on purpose" - not
"nobody thought of it yet." A future review that re-raises one of these
should link to the relevant ADR rather than re-litigate it as a fresh
finding.

| # | Title | Status |
|---|---|---|
| [0001](0001-logger-has-no-context-parameter.md) | Logger has no context.Context parameter | Accepted |
| [0002](0002-marshal-panic-log-keeps-the-original-errors-stack.md) | A marshal-panic log record keeps the original error's stack, not a second one | Accepted |
