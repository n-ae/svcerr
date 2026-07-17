package svcerr

import (
	"fmt"
	"net/http"
	"sync"
)

var (
	customStatusMu   sync.RWMutex
	customStatusCode = map[ErrorCode]int{}
)

// RegisterStatusCode adds or overrides the HTTP status HTTPStatusCode
// returns for code. Use it to extend the built-in mapping with an
// application's own ErrorCode values (constructed via New/Wrap), or to
// override a built-in mapping for a deployment that wants different
// semantics. Safe for concurrent use.
//
// status must be a valid error status (400-599, since this package only
// ever maps error codes to error responses) - an out-of-range value (0,
// 200, 999, ...) is rejected here rather than surfacing later as a
// WriteHeader panic from inside an error handler, which is a far harder
// place to diagnose it.
func RegisterStatusCode(code ErrorCode, status int) error {
	if status < 400 || status > 599 {
		return fmt.Errorf("svcerr: status must be 400-599, got %d", status)
	}
	customStatusMu.Lock()
	defer customStatusMu.Unlock()
	customStatusCode[code] = status
	return nil
}

// HTTPStatusCode maps error codes to HTTP status codes: the
// RegisterStatusCode registry first, then the built-in mapping. This is
// the mapping the package-level writers use; a Renderer consults its own
// StatusCodes instead of the registry (see RendererConfig).
func HTTPStatusCode(code ErrorCode) int {
	customStatusMu.RLock()
	status, ok := customStatusCode[code]
	customStatusMu.RUnlock()
	if ok {
		return status
	}
	return builtinStatusCode(code)
}

// builtinStatusCode is the registry-independent portion of
// HTTPStatusCode: the package's built-in code-to-status mapping. Split
// out so a Renderer can layer its own immutable StatusCodes over it
// without consulting (or being affected by) the mutable global registry.
func builtinStatusCode(code ErrorCode) int {
	switch code {
	// Validation errors -> 400 Bad Request
	case ErrCodeInvalidInput, ErrCodeMissingRequired, ErrCodeInvalidFormat, ErrCodeConstraintViolation:
		return http.StatusBadRequest

	// Authentication errors -> 401 Unauthorized or 403 Forbidden
	case ErrCodeUnauthorized, ErrCodeTokenExpired, ErrCodeTokenInvalid:
		return http.StatusUnauthorized
	case ErrCodePermissionDenied:
		return http.StatusForbidden

	// Resource errors -> 404 Not Found or 409 Conflict
	case ErrCodeNotFound:
		return http.StatusNotFound
	case ErrCodeAlreadyExists, ErrCodeResourceConflict:
		return http.StatusConflict

	// Rate limiting -> 429 Too Many Requests
	case ErrCodeRateLimitExceeded, ErrCodeQuotaExceeded:
		return http.StatusTooManyRequests

	// External API errors -> 502 Bad Gateway
	case ErrCodeExternalAPI:
		return http.StatusBadGateway

	// An unreachable database is a transient condition -> 503 Service
	// Unavailable. A malformed query or a failed transaction/migration is
	// a bug or invariant failure on this end, not something a client
	// retry fixes -> 500, same as any other internal error.
	case ErrCodeDatabaseConnection:
		return http.StatusServiceUnavailable
	case ErrCodeDatabaseQuery, ErrCodeDatabaseTransaction, ErrCodeDatabaseMigration:
		return http.StatusInternalServerError

	// Internal errors -> 500 Internal Server Error
	case ErrCodeInternal:
		return http.StatusInternalServerError

	// Not implemented -> 501 Not Implemented
	case ErrCodeNotImplemented:
		return http.StatusNotImplemented

	default:
		return http.StatusInternalServerError
	}
}
