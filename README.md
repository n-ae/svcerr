# svcerr

[![CI](https://github.com/n-ae/svcerr/actions/workflows/ci.yml/badge.svg)](https://github.com/n-ae/svcerr/actions/workflows/ci.yml)

Typed application errors for Go services: error codes, HTTP status mapping,
JSON/HTML response writers, panic-recovery middleware, and stack trace
capture. The core package imports no logging library — see "Logging"
below.

```go
import "github.com/n-ae/svcerr"
```

## Why

Handlers need to turn an error into the right HTTP status, a safe
user-facing message, and a structured log line, without leaking internals
(raw SQL, stack traces, third-party error text) into the response. `svcerr`
does that mapping once, centrally, instead of every handler improvising it.

## Usage

```go
func (s *Service) GetLeague(id string) (*League, error) {
	var league League
	err := s.db.QueryRow(`SELECT * FROM leagues WHERE id = ?`, id).Scan(&league)
	if err == sql.ErrNoRows {
		return nil, svcerr.NewNotFoundError("league", id)
	}
	if err != nil {
		return nil, svcerr.WrapDatabaseError(err, "query", "SELECT * FROM leagues...")
	}
	return &league, nil
}

func (h *Handler) GetLeague(w http.ResponseWriter, r *http.Request) {
	league, err := h.service.GetLeague(r.URL.Query().Get("id"))
	if err != nil {
		// h.logger implements svcerr.Logger - see "Logging" below
		svcerr.WriteHTTPError(w, err, h.logger) // maps to the right status, logs, writes JSON
		return
	}
	json.NewEncoder(w).Encode(league)
}
```

`WriteHTTPError` writes a JSON body:

```json
{
  "error": {
    "code": "NOT_FOUND",
    "message": "league not found: 12345",
    "details": { "resource_type": "league", "resource_id": "12345" }
  }
}
```

`WriteHTTPErrorHTML` writes an HTML fragment instead, for HTMX-style
endpoints. `WriteHTTPProblem` writes an
[RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) `application/problem+json`
body instead, for clients that expect the standard problem-details shape:

```json
{
  "type": "about:blank",
  "title": "Not Found",
  "status": 404,
  "detail": "league not found: 12345",
  "code": "NOT_FOUND",
  "resource_type": "league",
  "resource_id": "12345"
}
```

`title` is the HTTP status's standard reason phrase, per RFC 9457 §4.2.1
(since `type` is `"about:blank"` here); `detail` is specific to this
occurrence (and follows the same public/internal message rules as
`WriteHTTPError` - see "Public vs. internal messages" below). Extension
members (`code`, `resource_type`, `resource_id`, ...) sit at the top level,
per RFC 9457, rather than nested under a sub-object; see "Public details"
below for adding to or suppressing them.

`SetProblemType`/`SetProblemInstance` override `type`/`instance` on a
specific error instance, for an application with its own stable
problem-type URIs instead of `about:blank`:

```go
err := svcerr.NewNotFoundError("league", id)
err.SetProblemType("https://example.com/problems/resource-not-found")
err.SetProblemInstance(requestURL) // omitted entirely when unset
```

`title` stays the HTTP status's reason phrase regardless - there's no
per-error `Title` override on `WriteHTTPProblem`. A caller that wants a
custom `Title` alongside a custom `Type` can construct `ProblemDetails`
directly instead.

`UserMessage(err)` returns just the sanitized message, for callers
embedding it in a custom response.

Check error types with stdlib `errors.As` — there's no per-type `IsXError`
wrapper. `svcerr` is a distinct package name (not `errors`), so both imports
coexist without an alias:

```go
import (
	"errors"

	"github.com/n-ae/svcerr"
)

var nfErr *svcerr.NotFoundError
if errors.As(err, &nfErr) {
	// ...
}
```

Recover panics in HTTP handlers and turn them into a proper error response:

```go
router.Use(svcerr.RecoveryMiddleware(h.logger))
```

`RecoveryMiddleware` tracks whether the handler already wrote a response
before panicking, and won't write an error body over one that's already
committed - it just logs in that case. It also passes through
`http.Flusher` and `http.Hijacker`, so SSE handlers and WebSocket upgrades
still work for handlers wrapped by the middleware; a successful hijack is
itself treated as committing the response.

### Error types

`ValidationError`, `DatabaseError`, `ExternalAPIError`, `AuthenticationError`,
`NotFoundError`, `ConflictError`, `RateLimitError`, `InternalError` — each
has a `New*`/`Wrap*` constructor, carries an `ErrorCode`, and supports
stdlib `errors.Is`/`errors.As`/`errors.Unwrap` in the usual way. See the
package doc comment in [`errors.go`](errors.go) for the full list of codes
and their HTTP status mapping.

For a code with no dedicated constructor (e.g. `ErrCodeDatabaseConnection`,
`ErrCodeMissingRequired`, `ErrCodeResourceConflict`, `ErrCodeQuotaExceeded`),
use the generic `New`/`Wrap`:

```go
err := svcerr.New(svcerr.ErrCodeDatabaseConnection, "could not reach the database")
err := svcerr.Wrap(dbErr, svcerr.ErrCodeDatabaseConnection, "could not reach the database")
```

For an application-specific code entirely outside this package's built-in
set, register its HTTP status once, at startup:

```go
const ErrCodeOutOfStock svcerr.ErrorCode = "OUT_OF_STOCK"

func init() {
	if err := svcerr.RegisterStatusCode(ErrCodeOutOfStock, http.StatusConflict); err != nil {
		panic(err) // a bad registration at startup should fail loudly, not later inside an error handler
	}
}
```

`RegisterStatusCode` can also override a built-in code's mapping, for a
deployment that wants different semantics than the default. It rejects any
status outside 400-599, since this package only ever maps error codes to
error responses.

### Custom error types

You don't have to embed `BaseError` to plug into this package - implement
just the capability you need:

```go
type Coder interface {
	Code() ErrorCode // GetErrorCode, HTTPStatusCode
}

type StackTracer interface {
	StackTrace() []string // GetStackTrace
}

type PublicMessager interface {
	PublicMessage() (string, bool) // getUserFriendlyMessage / UserMessage overrides
}

type PublicDetailer interface {
	PublicDetails() (add map[string]interface{}, remove map[string]struct{}) // extractErrorDetails overrides
}

type ProblemTyper interface {
	ProblemType() (string, bool) // WriteHTTPProblem's "type", instead of "about:blank"
}

type ProblemInstancer interface {
	ProblemInstance() (string, bool) // WriteHTTPProblem's "instance"
}
```

A minimal type implementing `Coder` (and `error`) is enough to get correct
status-code mapping from `WriteHTTPError`/`WriteHTTPProblem`; add `Unwrap()
error` if you also want the "don't leak a wrapped cause" safety property.
`ErrorWithCode` is the full combination `BaseError`-derived types implement
(plus `StackTracer`), kept as a single name for convenience - it's not a
requirement for participating in the package's functions.

### Public vs. internal messages

By default, whether the client-facing message is the error's own message
depends on its **category**, not on whether it wraps a cause:

- **Validation, not-found, conflict, auth, and rate-limit** codes are
  client-input-shaped - the message you pass to `NewValidationError`,
  `WrapValidationError`, `NewNotFoundError`, etc. is always an explicit
  argument you chose, never text derived from a wrapped cause, so it's
  shown by default even when the error wraps one.
- **Database, external-API, and internal** codes fall back to a generic
  per-code message instead, whether or not they wrap a cause - `Error()`
  text in these categories often carries operational detail (a raw query,
  an internal hostname, third-party response text, a secret) that must
  never reach a client without an explicit opt-in.

`SetPublicMessage` overrides the client-facing text explicitly, for either
category:

```go
err := svcerr.WrapDatabaseError(dbErr, "query", "SELECT * FROM leagues...")
err.SetPublicMessage("We're having trouble reaching the database. Please try again shortly.")
return err // WriteHTTPError/UserMessage now send the override; logs still get err.Error()
```

### Public details

Built-in types automatically contribute a few structured fields to the
response's `details` map - `NotFoundError`'s `resource_type`/`resource_id`,
`ValidationError`'s `field`, `RateLimitError`'s `limit`/`retry_after`, and
so on (never anything caller-supplied and unbounded, like
`ValidationError.Value` - see the source for the exact list per type).
`SetPublicDetail`/`RemovePublicDetail` let you add to or suppress that on
a specific error instance, and are the only way to get structured details
at all for a code built with the generic `New`/`Wrap` (which have no
built-in type to extract from). Calling either again for the same key
later - even after the other one - changes your mind: whichever call was
most recent wins.

```go
err := svcerr.New(svcerr.ErrCodeConstraintViolation, "out of stock")
err.SetPublicDetail("sku", sku)
err.SetPublicDetail("available", 0)
```

**`RemovePublicDetail` only touches the `details` map - not the
message.** `NewNotFoundError(resourceType, resourceID)`'s own message
embeds `resourceID` directly (`"user not found: secret@example.com"`), and
`NOT_FOUND` is a category `mayExposeOwnMessage` shows by default (see
"Public vs. internal messages" above) - so `RemovePublicDetail` alone
still leaves the identifier in `message`. If the identifier itself is
sensitive, pair it with `SetPublicMessage`:

```go
err := svcerr.NewNotFoundError("user", customerEmail)
err.RemovePublicDetail("resource_id")     // removes it from details...
err.SetPublicMessage("user was not found") // ...and this removes it from message
```

### Wrapping constructors in your own helper

Every `New*`/`Wrap*` constructor assumes it's called directly from the site
its stack trace should point at. If you wrap one in your own helper (e.g. a
project-wide validation function), the trace ends up pointing at the helper
instead of its caller. Fix that with `RecaptureStackTrace`, called from
inside the helper right after construction:

```go
func validateTeamID(id string) error {
	if id == "" {
		err := svcerr.NewValidationError("team_id is required", "team_id", nil)
		svcerr.RecaptureStackTrace(err, 1) // point past this helper
		return err
	}
	return nil
}
```

### Concurrency

Errors aren't safe for concurrent mutation. `SetPublicMessage`,
`SetPublicDetail`, `RemovePublicDetail`, `SetProblemType`,
`SetProblemInstance`, and `RecaptureStackTrace` all mutate the receiver in
place with no locking. That's fine for the normal pattern - construct an
error, configure it, return it - but don't call these once an error might
be read or mutated from another goroutine.

### Logging

`WriteHTTPError`, `WriteHTTPErrorHTML`, `WriteHTTPProblem`, and
`RecoveryMiddleware` log through a minimal `Logger` interface, not a
specific logging library:

```go
type Logger interface {
	Log(level Level, err error, fields map[string]interface{}, msg string)
}
```

Using [zerolog](https://github.com/rs/zerolog)? Wrap it with the
`zerologadapter` subpackage instead of writing your own adapter:

```go
import "github.com/n-ae/svcerr/zerologadapter"

svcerr.WriteHTTPError(w, err, zerologadapter.New(logger))
```

`zerologadapter` is a separate Go module (its own `go.mod`, nested in this
repo at [`zerologadapter/`](zerologadapter)) - it's the one depending on
zerolog, not the core `svcerr` module. `go get github.com/n-ae/svcerr`
never pulls in zerolog; only `go get github.com/n-ae/svcerr/zerologadapter`
does, and only for callers who use it.

For any other logger, implement the one-method `Logger` interface directly.
A `nil` `Logger` is also fine - `WriteHTTPError`/`WriteHTTPErrorHTML`/
`WriteHTTPProblem`/`RecoveryMiddleware` simply skip logging rather than
panicking, so you don't have to plumb through a no-op implementation just
to render a response without logging it.

If you want response rendering with no `Logger` involvement at all - e.g.
you're reporting errors through something other than this package's
`Logger` contract - use `WriteJSON`, `WriteHTML`, or `WriteProblem`
directly. Each writes the same body as its `WriteHTTP*` counterpart and
returns the status code used, without touching a logger:

```go
statusCode := svcerr.WriteJSON(w, err) // no logging, no Logger argument
myReporter.Report(r.Context(), err, statusCode)
```

## Origin

Extracted from an application's `internal/errors` package once it had no
callers left depending on app-specific behavior — see the
[release notes](https://github.com/n-ae/svcerr/releases) for what changed
along the way. Note: `zerologadapter/v0.4.0` shipped with a `go.mod` that
made it unfetchable outside this repo; use `zerologadapter/v0.4.1` or
later instead (the root `svcerr` module's own `v0.4.0` tag is unaffected).
