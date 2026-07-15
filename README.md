# svcerr

[![CI](https://github.com/n-ae/svcerr/actions/workflows/ci.yml/badge.svg)](https://github.com/n-ae/svcerr/actions/workflows/ci.yml)

Typed application errors for Go services: error codes, HTTP status mapping,
JSON/HTML response writers, panic-recovery middleware, and stack trace
capture — with no dependency on any specific logging library.

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
		return nil, errors.NewNotFoundError("league", id)
	}
	if err != nil {
		return nil, errors.WrapDatabaseError(err, "query", "SELECT * FROM leagues...")
	}
	return &league, nil
}

func (h *Handler) GetLeague(w http.ResponseWriter, r *http.Request) {
	league, err := h.service.GetLeague(r.URL.Query().Get("id"))
	if err != nil {
		// h.logger implements errors.Logger - see "Logging" below
		errors.WriteHTTPError(w, err, h.logger) // maps to the right status, logs, writes JSON
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
endpoints. `UserMessage(err)` returns just the sanitized message, for
callers embedding it in a custom response.

Check error types with stdlib `errors.As` — there's no per-type `IsXError`
wrapper:

```go
var nfErr *errors.NotFoundError
if errors.As(err, &nfErr) {
	// ...
}
```

Recover panics in HTTP handlers and turn them into a proper error response:

```go
router.Use(errors.RecoveryMiddleware(h.logger))
```

### Error types

`ValidationError`, `DatabaseError`, `ExternalAPIError`, `AuthenticationError`,
`NotFoundError`, `ConflictError`, `RateLimitError`, `InternalError` — each
has a `New*`/`Wrap*` constructor, carries an `ErrorCode`, and supports
`errors.Is`/`errors.As`/`errors.Unwrap` in the usual way. See the package
doc comment in [`errors.go`](errors.go) for the full list of codes and their
HTTP status mapping.

### Public vs. internal messages

By default the client-facing message is either the error's own `Error()`
text or a generic per-code default — usually fine, but sometimes `Error()`
carries detail (a raw query, an internal hostname) you'd log but not want to
send back to a client. `SetPublicMessage` overrides just the client-facing
side for that one error instance; the logged error is untouched:

```go
err := errors.WrapDatabaseError(dbErr, "query", "SELECT * FROM leagues...")
err.SetPublicMessage("We're having trouble reaching the database. Please try again shortly.")
return err // WriteHTTPError/UserMessage now send the override; logs still get err.Error()
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
		err := errors.NewValidationError("team_id is required", "team_id", nil)
		errors.RecaptureStackTrace(err, 1) // point past this helper
		return err
	}
	return nil
}
```

### Logging

`WriteHTTPError`, `WriteHTTPErrorHTML`, and `RecoveryMiddleware` log through
a minimal `Logger` interface, not a specific logging library:

```go
type Logger interface {
	Log(level Level, err error, fields map[string]interface{}, msg string)
}
```

Using [zerolog](https://github.com/rs/zerolog)? Wrap it with the
`zerologadapter` subpackage instead of writing your own adapter:

```go
import "github.com/n-ae/svcerr/zerologadapter"

errors.WriteHTTPError(w, err, zerologadapter.New(logger))
```

For any other logger, implement the one-method `Logger` interface directly.

## Origin

Extracted from an application's `internal/errors` package once it had no
callers left depending on app-specific behavior — see the
[release notes](https://github.com/n-ae/svcerr/releases) for what changed
along the way.
