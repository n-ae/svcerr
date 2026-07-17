package svcerr

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"sync"
)

// HTTPErrorResponse represents a standardized HTTP error response
type HTTPErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains detailed error information for API responses
type ErrorDetail struct {
	Code    ErrorCode              `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

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

// HTTPStatusCode maps error codes to HTTP status codes
func HTTPStatusCode(code ErrorCode) int {
	customStatusMu.RLock()
	status, ok := customStatusCode[code]
	customStatusMu.RUnlock()
	if ok {
		return status
	}

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

// WriteResult reports what WriteJSONResult/WriteHTMLResult/
// WriteProblemResult actually did, for a caller that wants to detect a
// serialization or delivery failure without participating in this
// package's Logger contract. WriteHTTPError/WriteHTTPErrorHTML/
// WriteHTTPProblem carry the same information into their log fields
// (response_render_error, response_write_error, rendered_error_code)
// instead of returning it; the plain WriteJSON/WriteHTML/WriteProblem
// functions discard it entirely, same as before this type existed.
type WriteResult struct {
	// Status is the HTTP status code svcerr selected and passed to
	// w.WriteHeader - the fallback 500 on a marshal failure, not
	// necessarily err's own classification. It's what svcerr chose to
	// send, not a transport confirmation that the client received exactly
	// that status: a custom or third-party ResponseWriter could ignore it,
	// have already committed a different status earlier, transform it, or
	// panic during WriteHeader (see trackingResponseWriter.WriteHeader).
	Status int
	// RenderErr is the marshal error when the real body couldn't be
	// JSON-encoded and a generic fallback was substituted instead (nil
	// otherwise). Always nil from WriteHTMLResult, whose body is plain
	// string concatenation and can't fail to encode.
	RenderErr error
	// WriteErr is whatever the final w.Write returned (nil on a full
	// write) - a client disconnect, an expired deadline, or any other
	// transport failure during delivery. Unlike RenderErr, it doesn't
	// imply a different body was sent, only that delivery of the
	// intended one may have failed or been truncated.
	WriteErr error
}

// WriteHTTPError writes a standardized error response to the HTTP response writer
func WriteHTTPError(w http.ResponseWriter, err error, logger Logger) {
	statusCode, renderErr, writeErr := writeJSONErrorBody(w, err)
	logError(logger, err, statusCode, renderErr, writeErr)
}

// WriteJSON writes err's standardized JSON error response to w and returns
// the HTTP status code used - the same body WriteHTTPError writes, minus
// the logging call, for a caller that wants to own reporting separately
// (its own Reporter, a nil Logger via WriteHTTPError, or none at all)
// instead of participating in this package's Logger contract just to
// render a response. Use WriteJSONResult instead to also see a
// render/write failure this discards.
func WriteJSON(w http.ResponseWriter, err error) int {
	return WriteJSONResult(w, err).Status
}

// WriteJSONResult mirrors WriteJSON, additionally reporting a render or
// write failure - e.g. so a caller can avoid claiming success to its own
// caller, or report the failure through its own error-tracking system
// instead of this package's Logger contract.
func WriteJSONResult(w http.ResponseWriter, err error) WriteResult {
	statusCode, renderErr, writeErr := writeJSONErrorBody(w, err)
	return WriteResult{Status: statusCode, RenderErr: renderErr, WriteErr: writeErr}
}

// checkedWrite writes body to w, returning an error if either Write itself
// failed or Write returned fewer bytes than len(body) with a nil error - a
// short write with no error violates io.Writer's documented contract
// ("Write must return a non-nil error if it returns n < len(p)"), which
// every real net/http-backed writer already honors. This guards
// specifically against a non-conforming custom writer, test double, or
// future adapter that violates that contract and would otherwise have a
// truncated body silently treated as a fully-delivered response.
func checkedWrite(w http.ResponseWriter, body []byte) error {
	n, err := w.Write(body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	return err
}

// writeJSONErrorBody writes err's JSON body and headers to w and returns
// the status code used, without logging, plus the marshal error when the
// real body couldn't be encoded and a generic fallback was substituted
// instead (nil otherwise), and any error from the final w.Write (nil on a
// full write) - the caller decides what to do with either (log them, in
// WriteHTTPError's case). Split out of WriteHTTPError so RecoveryMiddleware
// can write the response and log the panic as a single record instead of
// the response write and the log call each logging independently.
func writeJSONErrorBody(w http.ResponseWriter, err error) (statusCode int, renderErr, writeErr error) {
	code := GetErrorCode(err)
	statusCode = HTTPStatusCode(code)
	node := outermostCoded(err)

	errResp := HTTPErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: getUserFriendlyMessage(code, err),
			Details: extractErrorDetails(err),
		},
	}

	// Marshal before committing anything, so a value that can't be
	// JSON-encoded (a channel, a func, a cyclic structure passed to
	// SetPublicDetail, ...) doesn't leave a status already written and an
	// empty or truncated body - the caller would see a "successful" write
	// with no way to know the document is broken.
	body, marshalErr := json.Marshal(errResp)
	if marshalErr != nil {
		statusCode = http.StatusInternalServerError
		body = fallbackErrorBody(ErrCodeInternal)
		renderErr = marshalErr
	}

	prepareErrorHeaders(w.Header(), "application/json")

	// Skipped on the marshal-failure fallback: that response no longer
	// represents err's own classification.
	if marshalErr == nil {
		rateLimitRetryAfterHeader(w.Header(), node)
	}

	setAuthenticateChallenge(w.Header(), statusCode, node)

	w.WriteHeader(statusCode)
	writeErr = checkedWrite(w, body)

	return statusCode, renderErr, writeErr
}

// prepareErrorHeaders resets the response headers this package's writers
// need to be correct, in case the handler already set headers expecting a
// successful response before panicking or returning an error - net/http's
// own http.Error does the same for Content-Length, for the same reason.
// Content-Length is deleted because the body about to be written is a
// different size than whatever the handler may have declared, and a stale
// value can cause client-side truncation or a real server's
// ResponseWriter to reject or truncate the write. Trailer is deleted
// because any trailers it announced won't be sent, since this response
// has none. Content-Encoding is deleted because the body these writers
// produce is always plain, uncompressed text - a handler that set it
// while planning to write compressed bytes itself (rather than relying on
// an outer compression middleware that transparently wraps the
// ResponseWriter and sets the header itself after compressing whatever's
// written) would otherwise leave clients trying to gzip-decode a body
// that was never actually compressed.
//
// Retry-After and WWW-Authenticate are deleted for the same reason as
// Content-Length: both describe the specific response this call is
// replacing, not necessarily this one - a handler (or a previous request
// through a reused/pooled ResponseWriter-like object) may have set either
// in anticipation of a response that never got written before this error
// took over. Every caller of prepareErrorHeaders re-adds Retry-After
// (rateLimitRetryAfterHeader) or WWW-Authenticate (setAuthenticateChallenge)
// immediately afterward when err's own classification actually calls for
// it, so a genuinely-applicable value is never lost.
//
// This still deliberately doesn't touch ETag, Last-Modified, or
// Accept-Ranges - those describe a specific successful representation
// this response isn't attempting to be, but aren't actively misleading
// the way a wrong Content-Length or Content-Encoding is.
func prepareErrorHeaders(h http.Header, contentType string) {
	h.Del("Content-Length")
	h.Del("Content-Encoding")
	h.Del("Trailer")
	h.Del("Retry-After")
	h.Del("WWW-Authenticate")
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
}

// rateLimitRetryAfterHeader sets Retry-After when node (the same
// outermost-coded node used for everything else in err's classification)
// is a *RateLimitError, so an outer wrapper's code can't inherit a wrapped
// RateLimitError's header. Shared by all three response writers; skipped
// on the marshal-failure fallback by the JSON/problem+json callers, since
// that response no longer represents err's own classification.
//
// RetryAfter is re-clamped here, at the wire boundary, because it's an
// exported writable field: the constructors' clampRetryAfter is only
// input cleanup, and a negative value assigned after construction would
// otherwise become an invalid Retry-After (RFC 9110 §10.2.3 requires a
// non-negative delay-seconds). extractErrorDetails and errorLogFields
// clamp the same way, so the header, the JSON details, and the log field
// always agree.
func rateLimitRetryAfterHeader(h http.Header, node coderError) {
	if rle, ok := node.(*RateLimitError); ok {
		h.Set("Retry-After", fmt.Sprintf("%d", clampRetryAfter(rle.RetryAfter)))
	}
}

var (
	defaultAuthMu        sync.RWMutex
	defaultAuthChallenge string
)

// SetDefaultAuthenticateChallenge sets an application-wide WWW-Authenticate
// challenge (e.g. `Bearer realm="api"`) that every 401 response from this
// package's writers carries when the error itself doesn't provide one via
// SetAuthenticateChallenge - an error-specific challenge always wins over
// this default. RFC 9110 §11.6.1 requires at least one WWW-Authenticate
// challenge on every server-generated 401 response; this package can't
// invent an application's authentication scheme or realm on its own, so
// without this call (or per-error challenges) a bare 401 remains possible.
// Set it once at startup, like RegisterStatusCode; the empty string clears
// it. Safe for concurrent use.
func SetDefaultAuthenticateChallenge(challenge string) {
	defaultAuthMu.Lock()
	defaultAuthChallenge = challenge
	defaultAuthMu.Unlock()
}

// setAuthenticateChallenge sets the WWW-Authenticate header when
// statusCode is 401: the challenge from node (the same outermost coded
// node used for everything else in err's classification) via
// Authenticator if it provides one, else the application-wide
// SetDefaultAuthenticateChallenge value if set, else nothing - RFC 9110
// §11.6.1 requires at least one WWW-Authenticate challenge on every 401
// response, but this package has no way to invent an application's
// authentication scheme or realm on its own, so both sources are opt-in.
// Shared by all three response writers (JSON, HTML, problem+json).
func setAuthenticateChallenge(h http.Header, statusCode int, node coderError) {
	if statusCode != http.StatusUnauthorized {
		return
	}
	if a, ok := node.(Authenticator); ok {
		if challenge, set := a.AuthenticateChallenge(); set {
			h.Set("WWW-Authenticate", challenge)
			return
		}
	}
	defaultAuthMu.RLock()
	challenge := defaultAuthChallenge
	defaultAuthMu.RUnlock()
	if challenge != "" {
		h.Set("WWW-Authenticate", challenge)
	}
}

// fallbackErrorBody returns the always-encodable JSON body writeJSONErrorBody
// substitutes when the real response failed to marshal - built from
// defaultMessageForCode's plain per-code string, never from err or any
// caller-supplied detail value, so json.Marshal here cannot itself fail.
func fallbackErrorBody(code ErrorCode) []byte {
	body, _ := json.Marshal(HTTPErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: defaultMessageForCode(code),
		},
	})
	return body
}

// WriteHTTPErrorHTML writes an HTML error response (for non-API endpoints)
func WriteHTTPErrorHTML(w http.ResponseWriter, err error, logger Logger) {
	statusCode, writeErr := writeHTMLErrorBody(w, err)
	logError(logger, err, statusCode, nil, writeErr)
}

// WriteHTML mirrors WriteJSON for the HTML rendering WriteHTTPErrorHTML
// writes. Use WriteHTMLResult instead to also see a write failure this
// discards.
func WriteHTML(w http.ResponseWriter, err error) int {
	return WriteHTMLResult(w, err).Status
}

// WriteHTMLResult mirrors WriteJSONResult for the HTML rendering.
// RenderErr is always nil - see WriteResult.
func WriteHTMLResult(w http.ResponseWriter, err error) WriteResult {
	statusCode, writeErr := writeHTMLErrorBody(w, err)
	return WriteResult{Status: statusCode, WriteErr: writeErr}
}

// writeHTMLErrorBody mirrors writeJSONErrorBody for the HTML response.
func writeHTMLErrorBody(w http.ResponseWriter, err error) (statusCode int, writeErr error) {
	code := GetErrorCode(err)
	statusCode = HTTPStatusCode(code)
	message := getUserFriendlyMessage(code, err)
	node := outermostCoded(err)

	prepareErrorHeaders(w.Header(), "text/html; charset=utf-8")
	rateLimitRetryAfterHeader(w.Header(), node)
	setAuthenticateChallenge(w.Header(), statusCode, node)
	w.WriteHeader(statusCode)

	body := `<div class="error-message" role="alert">` +
		`<h3>Error</h3>` +
		`<p>` + html.EscapeString(message) + `</p>` +
		`</div>`

	writeErr = checkedWrite(w, []byte(body))

	return statusCode, writeErr
}

// ProblemDetails is the RFC 9457 (https://www.rfc-editor.org/rfc/rfc9457)
// "application/problem+json" response body written by WriteHTTPProblem.
// Code and any extractErrorDetails fields are extension members - RFC 9457
// says extension members live at the top level alongside the registered
// ones, which is what MarshalJSON does instead of nesting them.
type ProblemDetails struct {
	Type       string                 // a URI reference identifying the problem type; "about:blank" when none is registered
	Title      string                 // a short, occurrence-invariant summary of the problem type
	Status     int                    // the HTTP status code for this occurrence
	Detail     string                 // a human-readable explanation specific to this occurrence
	Instance   string                 // a URI reference identifying this specific occurrence, if known
	Code       ErrorCode              // this package's own error code, as an extension member
	Extensions map[string]interface{} // additional extension members (e.g. resource_id, field)
}

// reservedProblemMembers are the member names MarshalJSON owns: the RFC
// 9457 §3.1 registered members plus this package's own "code" extension.
// An Extensions entry with one of these names is dropped rather than
// flattened - extension members live alongside the registered ones (RFC
// 9457 §3.2), they can't replace them. That matters even for the optional
// members omitted when empty: without the reservation, an extension named
// "instance" (e.g. via SetPublicDetail) could occupy the registered slot
// with a non-URI value that §3.1 obliges consumers to ignore.
var reservedProblemMembers = map[string]struct{}{
	"type":     {},
	"title":    {},
	"status":   {},
	"detail":   {},
	"instance": {},
	"code":     {},
}

// MarshalJSON flattens Extensions into the top-level object rather than
// nesting them under a sub-object, per RFC 9457's extension-member model.
// Extension entries named after a registered member (or "code") are
// dropped - see reservedProblemMembers.
func (p ProblemDetails) MarshalJSON() ([]byte, error) {
	out := make(map[string]interface{}, len(p.Extensions)+5)
	for k, v := range p.Extensions {
		if _, reserved := reservedProblemMembers[k]; reserved {
			continue
		}
		out[k] = v
	}
	out["type"] = p.Type
	out["title"] = p.Title
	out["status"] = p.Status
	if p.Detail != "" {
		out["detail"] = p.Detail
	}
	if p.Instance != "" {
		out["instance"] = p.Instance
	}
	out["code"] = p.Code
	return json.Marshal(out)
}

// WriteHTTPProblem writes an RFC 9457 "application/problem+json" error
// response - an alternative body shape to WriteHTTPError's own
// {"error": {...}} for callers whose clients expect the standard
// problem-details format. Status mapping, message safety (Detail never
// includes a wrapped cause's text without an explicit SetPublicMessage),
// and logging behave identically to WriteHTTPError.
func WriteHTTPProblem(w http.ResponseWriter, err error, logger Logger) {
	statusCode, renderErr, writeErr := writeProblemJSONBody(w, err)
	logError(logger, err, statusCode, renderErr, writeErr)
}

// WriteProblem mirrors WriteJSON for the RFC 9457 rendering
// WriteHTTPProblem writes. Use WriteProblemResult instead to also see a
// render/write failure this discards.
func WriteProblem(w http.ResponseWriter, err error) int {
	return WriteProblemResult(w, err).Status
}

// WriteProblemResult mirrors WriteJSONResult for the RFC 9457 rendering.
func WriteProblemResult(w http.ResponseWriter, err error) WriteResult {
	statusCode, renderErr, writeErr := writeProblemJSONBody(w, err)
	return WriteResult{Status: statusCode, RenderErr: renderErr, WriteErr: writeErr}
}

// writeProblemJSONBody mirrors writeJSONErrorBody for the problem+json body.
func writeProblemJSONBody(w http.ResponseWriter, err error) (statusCode int, renderErr, writeErr error) {
	code := GetErrorCode(err)
	statusCode = HTTPStatusCode(code)
	node := outermostCoded(err)

	problemType := "about:blank"
	if pt, ok := node.(ProblemTyper); ok {
		if uri, set := pt.ProblemType(); set {
			problemType = uri
		}
	}
	var instance string
	if pi, ok := node.(ProblemInstancer); ok {
		instance, _ = pi.ProblemInstance()
	}

	// RFC 9457 4.2.1: when type is "about:blank", title SHOULD be the
	// same as the HTTP status's reason phrase (e.g. "Not Found" for 404) -
	// the occurrence-specific text belongs in Detail, not Title. That's
	// also a reasonable default alongside a custom Type, but
	// SetProblemTitle overrides it for a caller who wants a title that
	// actually describes their custom problem type rather than the HTTP
	// status in general.
	title := http.StatusText(statusCode)
	if pt, ok := node.(ProblemTitler); ok {
		if custom, set := pt.ProblemTitle(); set {
			title = custom
		}
	}

	problem := ProblemDetails{
		Type:       problemType,
		Title:      title,
		Status:     statusCode,
		Detail:     getUserFriendlyMessage(code, err),
		Instance:   instance,
		Code:       code,
		Extensions: extractErrorDetails(err),
	}

	body, marshalErr := json.Marshal(problem)
	if marshalErr != nil {
		statusCode = http.StatusInternalServerError
		body = fallbackProblemBody(statusCode)
		renderErr = marshalErr
	}

	prepareErrorHeaders(w.Header(), "application/problem+json")

	if marshalErr == nil {
		rateLimitRetryAfterHeader(w.Header(), node)
	}

	setAuthenticateChallenge(w.Header(), statusCode, node)

	w.WriteHeader(statusCode)
	writeErr = checkedWrite(w, body)

	return statusCode, renderErr, writeErr
}

// fallbackProblemBody mirrors fallbackErrorBody for the problem+json body -
// built from fixed fields and http.StatusText, never from err or any
// caller-supplied detail value, so json.Marshal here cannot itself fail.
func fallbackProblemBody(statusCode int) []byte {
	body, _ := json.Marshal(ProblemDetails{
		Type:   "about:blank",
		Title:  http.StatusText(statusCode),
		Status: statusCode,
		Code:   ErrCodeInternal,
	})
	return body
}

// UserMessage returns the safe, user-facing message for an error - the same
// sanitized text WriteHTTPError/WriteHTTPErrorHTML send (e.g. a wrapped
// database error's raw cause is never included), for callers that need to
// embed it in a custom response fragment instead of one of those two
// standard bodies.
func UserMessage(err error) string {
	return getUserFriendlyMessage(GetErrorCode(err), err)
}

// mayExposeOwnMessage reports whether an error carrying code is safe to
// show its own message as public-facing text, absent an explicit
// SetPublicMessage override. Client-input-shaped categories - validation,
// not-found, conflict, auth, rate-limiting - are written by the calling
// code specifically to be read by the client (e.g. NewValidationError's
// message, or WrapValidationError's - both are an explicit argument the
// caller chose, never derived from the wrapped cause), so they're safe by
// default. Database, external-API, and internal errors often carry
// operational detail (queries, hosts, upstream response bodies) even in
// their own message, so those always fall back to the generic per-code
// message unless the caller opts in via SetPublicMessage.
func mayExposeOwnMessage(code ErrorCode) bool {
	switch code {
	case ErrCodeInvalidInput, ErrCodeMissingRequired, ErrCodeInvalidFormat, ErrCodeConstraintViolation,
		ErrCodeUnauthorized, ErrCodeTokenExpired, ErrCodeTokenInvalid, ErrCodePermissionDenied,
		ErrCodeNotFound, ErrCodeAlreadyExists, ErrCodeResourceConflict,
		ErrCodeRateLimitExceeded, ErrCodeQuotaExceeded:
		return true
	default:
		return false
	}
}

// getUserFriendlyMessage returns a user-friendly error message
func getUserFriendlyMessage(code ErrorCode, err error) string {
	if err == nil {
		return defaultMessageForCode(code)
	}

	// Both the public-message override and the own-message fallback below
	// come from the same outermost coded node the code itself came from -
	// otherwise a custom Coder-only wrapper (one that doesn't implement
	// PublicMessager) around an error with SetPublicMessage set would let
	// errors.As find that inner override and pair it with the outer
	// wrapper's own, different code.
	node := outermostCoded(err)
	if node == nil {
		return defaultMessageForCode(code)
	}

	// An explicit SetPublicMessage override always wins.
	if pm, ok := node.(PublicMessager); ok {
		if msg, ok := pm.PublicMessage(); ok {
			return msg
		}
	}

	// Only the outermost coded node's own message - never Error(),
	// which would append a wrapped cause's text - and only for
	// categories mayExposeOwnMessage trusts by default.
	if mayExposeOwnMessage(code) {
		if m, ok := node.(ownMessager); ok {
			return m.ownMessage()
		}
		// node doesn't implement ownMessage (e.g. an external Coder
		// type that doesn't embed BaseError) - fall back to the same
		// safety rule ownMessage replaces for this package's own
		// types: Error() is only trusted when the node doesn't wrap a
		// further cause, since without an ownMessage accessor there's
		// no way to know its Error() text excludes the cause.
		if u, ok := node.(interface{ Unwrap() error }); ok && u.Unwrap() == nil {
			return node.Error()
		}
	}

	return defaultMessageForCode(code)
}

// defaultMessageForCode returns the generic, occurrence-invariant
// client-facing message for code - getUserFriendlyMessage's fallback when
// an error's own message can't be shown, and the whole body of
// fallbackErrorBody's always-encodable substitute. (WriteHTTPProblem's
// RFC 9457 "title" member is not this: it's http.StatusText(status), or a
// ProblemTitler override - see writeProblemJSONBody.)
func defaultMessageForCode(code ErrorCode) string {
	switch code {
	case ErrCodeInvalidInput, ErrCodeInvalidFormat, ErrCodeConstraintViolation:
		return "Invalid input provided. Please check your request and try again."
	case ErrCodeMissingRequired:
		return "Required field is missing."
	case ErrCodeUnauthorized:
		return "Authentication required. Please log in."
	case ErrCodeTokenExpired:
		return "Your session has expired. Please log in again."
	case ErrCodeTokenInvalid:
		return "Invalid authentication token."
	case ErrCodePermissionDenied:
		return "You don't have permission to access this resource."
	case ErrCodeNotFound:
		return "The requested resource was not found."
	case ErrCodeAlreadyExists:
		return "A resource with this identifier already exists."
	case ErrCodeResourceConflict:
		return "The request conflicts with the current state of the resource."
	case ErrCodeRateLimitExceeded:
		return "Too many requests. Please try again later."
	case ErrCodeQuotaExceeded:
		return "You have exceeded your allotted quota."
	case ErrCodeExternalAPI:
		return "External service is temporarily unavailable. Please try again later."
	case ErrCodeDatabaseConnection, ErrCodeDatabaseQuery, ErrCodeDatabaseTransaction, ErrCodeDatabaseMigration:
		return "Database error occurred. Please try again."
	case ErrCodeInternal:
		return "An internal error occurred. Please contact support if the problem persists."
	case ErrCodeNotImplemented:
		return "This functionality is not yet implemented."
	default:
		return "An unexpected error occurred."
	}
}

// extractErrorDetails extracts contextual details from the outermost coded
// error in err's chain - the same node whose code selects the HTTP status
// and message (see outermostCoded) - so a wrapper's code is never paired
// with a wrapped error's details.
func extractErrorDetails(err error) map[string]interface{} {
	details := make(map[string]interface{})
	node := outermostCoded(err)

	switch v := node.(type) {
	case *ValidationError:
		if v.Field != "" {
			details["field"] = v.Field
		}
		// v.Value is deliberately not included here - it's whatever the
		// caller passed in (a password, a token, an oversized payload),
		// and this package has no way to know it's safe to publish.
	case *DatabaseError:
		if v.Operation != "" {
			details["operation"] = v.Operation
		}
	case *ExternalAPIError:
		details["service"] = v.Service
		if v.StatusCode > 0 {
			details["status_code"] = v.StatusCode
		}
		if v.RetryAfter != nil {
			// Clamped like RateLimitError's: this field is documented for
			// direct post-construction assignment, so no constructor ever
			// vetted it.
			details["retry_after"] = clampRetryAfter(*v.RetryAfter)
		}
	case *NotFoundError:
		details["resource_type"] = v.ResourceType
		if v.ResourceID != "" {
			details["resource_id"] = v.ResourceID
		}
	case *RateLimitError:
		details["limit"] = v.Limit
		// Re-clamped at emission - see rateLimitRetryAfterHeader.
		details["retry_after"] = clampRetryAfter(v.RetryAfter)
	}

	// SetPublicDetail/RemovePublicDetail overrides, from the same
	// outermost coded node the built-in extraction above came from -
	// applied after it, so an addition can override a built-in key and a
	// removal can suppress one.
	if pd, ok := node.(PublicDetailer); ok {
		add, remove := pd.PublicDetails()
		for k, v := range add {
			details[k] = v
		}
		for k := range remove {
			delete(details, k)
		}
	}

	if len(details) == 0 {
		return nil
	}
	return details
}

// errorLogFields builds the level and structured fields describing err at
// the given status code. Shared by logError and RecoveryMiddleware, so a
// recovered panic produces one log record carrying both the panic context
// and the usual error fields, rather than each logging independently.
func errorLogFields(err error, statusCode int) (Level, map[string]interface{}) {
	// Determine log level based on status code
	level := LevelInfo
	switch {
	case statusCode >= 500:
		level = LevelError
	case statusCode >= 400:
		level = LevelWarn
	}

	code := GetErrorCode(err)
	fields := map[string]interface{}{
		"error_code":  string(code),
		"http_status": statusCode,
	}

	// Add stack trace for server errors
	if statusCode >= 500 {
		if stack := GetStackTrace(err); len(stack) > 0 {
			fields["stack_trace"] = stack
		}
	}

	// Add type-specific context from the same outermost coded node code
	// itself came from (via GetErrorCode/outermostCoded above) - not an
	// independent errors.As search across the whole chain, which could
	// find and attribute fields from a different (inner) node than the one
	// that produced code/statusCode. extractErrorDetails already follows
	// this rule for the client-facing response body; this mirrors it for
	// logging, for the same reason its own doc comment gives: an outer
	// wrapper's code (e.g. ErrCodeNotFound from WrapNotFoundError) must
	// not end up paired with a wrapped error's details (e.g. a
	// DatabaseError's operation).
	switch v := outermostCoded(err).(type) {
	case *ValidationError:
		fields["field"] = v.Field
	case *DatabaseError:
		fields["db_operation"] = v.Operation
	case *ExternalAPIError:
		fields["service"] = v.Service
		fields["service_status"] = v.StatusCode
	case *AuthenticationError:
		fields["auth_reason"] = v.Reason
	case *NotFoundError:
		fields["resource_type"] = v.ResourceType
		fields["resource_id"] = v.ResourceID
	case *ConflictError:
		fields["resource_type"] = v.ResourceType
		fields["conflict_key"] = v.ConflictKey
	case *RateLimitError:
		fields["service"] = v.Service
		fields["limit"] = v.Limit
		// Clamped to match what the response writers actually sent - see
		// rateLimitRetryAfterHeader.
		fields["retry_after"] = clampRetryAfter(v.RetryAfter)
	case *InternalError:
		fields["component"] = v.Component
	}

	return level, fields
}

// logError logs error with appropriate level and context. renderErr is
// the marshal error from writeJSONErrorBody/writeProblemJSONBody when the
// real response body couldn't be encoded and a generic fallback was
// substituted (nil otherwise) - logged as its own field, together with
// the code the client actually received, so the log doesn't just show
// err's original classification with no indication the client got a
// different one. writeErr is whatever the final w.Write returned (nil on
// a full write) - unlike renderErr it doesn't imply a different body was
// sent, only that delivery of the intended one may have failed or been
// truncated (client disconnect, expired deadline, ...), which is
// otherwise invisible: WriteHeader/Write don't return to their caller in
// a way any of this package's writers surface today.
func logError(logger Logger, err error, statusCode int, renderErr, writeErr error) {
	level, fields := errorLogFields(err, statusCode)
	if renderErr != nil {
		fields["response_render_error"] = renderErr.Error()
		fields["rendered_error_code"] = string(ErrCodeInternal)
	}
	if writeErr != nil {
		fields["response_write_error"] = writeErr.Error()
	}
	safeLog(logger, level, err, fields, "HTTP error response")
}

// safeLog calls logger.Log if logger is non-nil, containing a panic from
// within that call so it can't escape and replace whatever this package
// was itself in the middle of reporting. That matters most in
// RecoveryMiddleware: its own recover() has already fired once for the
// handler's original panic by the time it calls safeLog, so a second,
// uncaught panic from a broken Logger would propagate out of that
// already-executing deferred function - past this package entirely,
// caught only by net/http's own outer per-connection recovery, which
// drops the original panic's structured log record (error code, stack
// trace, request path) and prints a generic stdlib trace pointing at the
// logger instead. There's nowhere further to report a logger's own
// failure to - the logger is what would have received that report - so
// it's contained and silently dropped rather than escalated.
//
// A nil Logger is tolerated (not an error) everywhere this package logs,
// so WriteHTTPError/WriteHTTPErrorHTML/WriteHTTPProblem/RecoveryMiddleware
// stay usable by a caller that doesn't want logging at all, without
// forcing them to plumb through a no-op implementation just to avoid a
// nil-pointer panic. Callers who want response rendering with no logging
// contract whatsoever can use WriteJSON/WriteHTML/WriteProblem directly
// instead of passing nil here.
func safeLog(logger Logger, level Level, err error, fields map[string]interface{}, msg string) {
	if logger == nil {
		return
	}
	defer func() { _ = recover() }()
	logger.Log(level, err, fields, msg)
}

// trackingResponseWriter records whether a response has already been
// committed (a header or body written), so RecoveryMiddleware can tell
// whether it's still safe to write an error response after a panic. It
// implements only http.ResponseWriter itself - Flush and Hijack are added
// by the separate wrapper types below (flushTracker, hijackTracker,
// flushHijackTracker), chosen by newTrackingResponseWriter to match
// exactly what the underlying writer supports. A single type that always
// implemented both regardless of what's underneath would misrepresent the
// underlying writer's real capabilities to a handler's own
// w.(http.Flusher)/w.(http.Hijacker) assertions, and to
// http.ResponseController, which would see a false "supported" instead of
// discovering the truth by unwrapping.
type trackingResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
	status      int
}

func (w *trackingResponseWriter) WriteHeader(status int) {
	// Validate the status here, before recording or delegating anything -
	// the same 100-999 range net/http's own checkWriteHeaderCode accepts.
	// Panicking pre-commitment keeps an invalid status recoverable:
	// nothing reached the connection, so RecoveryMiddleware's recovery can
	// still take its "uncommitted" branch and write a real, valid error
	// response. Before this validation existed, the same outcome depended
	// on delegating first and letting the underlying writer's own
	// validation panic - which forced commitment to be recorded after the
	// delegate call returned, leaving the opposite gap described below.
	if status < 100 || status > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", status))
	}
	// Informational (1xx) responses aren't the final response - net/http
	// allows any number of them before the one commit-worthy final
	// status ("unlike other response headers, informational headers may
	// be written multiple times"), so they must not mark the tracked
	// response committed; a handler that sends one and then panics still
	// needs RecoveryMiddleware to write the real error response. 101
	// Switching Protocols is the exception: it's a protocol transition,
	// not an informational preamble, and no further HTTP response
	// follows on the connection.
	if status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.wroteHeader {
		return
	}
	// Record commitment before delegating, conservatively assuming a
	// valid delegated call may commit before panicking: an intermediate
	// writer sitting between RecoveryMiddleware and the transport whose
	// WriteHeader delegates downstream and then panics (a buggy metrics
	// wrapper, say) never returns here, so recording afterward would
	// leave the response looking uncommitted and let recovery write a
	// second error document onto a status and headers already sent. The
	// cost is the opposite, rarer case: a delegate that panics on a valid
	// status without committing anything is now treated as committed, so
	// recovery aborts the connection (http.ErrAbortHandler) instead of
	// writing a clean 500. Aborting a connection the client can retry is
	// strictly safer than corrupting a response that may already be on
	// the wire, so the conservative direction wins. Statuses a real
	// writer rejects don't pay this cost - they panic in the validation
	// above, before commitment is recorded.
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *trackingResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap exposes the wrapped ResponseWriter to http.ResponseController,
// which looks for this method (or the original ResponseWriter itself) to
// reach capabilities like SetReadDeadline/SetWriteDeadline through a
// wrapper such as this one.
func (w *trackingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// commitOnFlush marks tw committed (implicitly as 200 OK, the same as
// Write) before delegating to f.Flush - shared by flushTracker and
// flushHijackTracker, the only two variants that ever call it (both are
// only constructed when the underlying writer actually supports
// http.Flusher). A successful flush commits the response the same way
// Write does, so it's recorded the same way - otherwise RecoveryMiddleware
// could still believe the response is uncommitted after a flush and write
// a second, corrupting body on top of it if the handler subsequently
// panics.
func commitOnFlush(tw *trackingResponseWriter, f http.Flusher) {
	if !tw.wroteHeader {
		tw.wroteHeader = true
		tw.status = http.StatusOK
	}
	f.Flush()
}

// flushErrorer is the optional method http.ResponseController prefers
// over plain http.Flusher, for an underlying writer that can report a
// flush failure - http.Flusher's Flush() has no return value and so no
// way to signal one. Documented by http.NewResponseController; not a
// named type in net/http.
type flushErrorer interface {
	FlushError() error
}

// commitOnFlushError marks tw committed before delegating to
// fe.FlushError(), matching what a real FlushError implementation - the
// stdlib HTTP/1.1 server's response.FlushError, and anything wrapping it -
// actually does: WriteHeader(200) is sent unconditionally before the
// flush is even attempted, so a flush failure (a broken connection
// partway through) can happen after the status line and headers are
// already on the wire. Marking committed only on success, as if the
// commitment itself were conditional on the flush succeeding, would leave
// RecoveryMiddleware believing it's still safe to write a fresh JSON
// error body - a second response onto a connection that may have already
// received the first one's headers.
func commitOnFlushError(tw *trackingResponseWriter, fe flushErrorer) error {
	if !tw.wroteHeader {
		tw.wroteHeader = true
		tw.status = http.StatusOK
	}
	return fe.FlushError()
}

// commitOnHijack hijacks through hj, marking tw committed on success -
// shared by hijackTracker and flushHijackTracker, the only two variants
// that ever call it (both are only constructed when the underlying writer
// actually supports http.Hijacker). A successful hijack hands the raw
// connection to the caller, so it's treated as committing the response -
// RecoveryMiddleware must never attempt to write a JSON error body onto a
// hijacked connection.
func commitOnHijack(tw *trackingResponseWriter, hj http.Hijacker) (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := hj.Hijack()
	if err == nil {
		tw.wroteHeader = true
	}
	return conn, rw, err
}

// flushTracker adds http.Flusher to trackingResponseWriter, for an
// underlying writer that supports plain flushing (but not FlushError or
// hijacking).
type flushTracker struct {
	*trackingResponseWriter
	flusher http.Flusher
}

func (w *flushTracker) Flush() { commitOnFlush(w.trackingResponseWriter, w.flusher) }

// flushErrorTracker adds http.Flusher and FlushError() error to
// trackingResponseWriter, for an underlying writer that reports flush
// failures (but doesn't support hijacking). Flush() discards the error -
// http.Flusher's signature has no way to report one - but still delegates
// through FlushError so a real failure isn't treated as a successful
// commit; FlushError() itself is what http.ResponseController actually
// calls (it checks for this method before plain Flusher), which is the
// entire reason this variant exists separately from flushTracker.
type flushErrorTracker struct {
	*trackingResponseWriter
	flushErrorer flushErrorer
}

func (w *flushErrorTracker) Flush() {
	_ = commitOnFlushError(w.trackingResponseWriter, w.flushErrorer)
}

func (w *flushErrorTracker) FlushError() error {
	return commitOnFlushError(w.trackingResponseWriter, w.flushErrorer)
}

// hijackTracker adds http.Hijacker to trackingResponseWriter, for an
// underlying writer that supports hijacking but not flushing.
type hijackTracker struct {
	*trackingResponseWriter
	hijacker http.Hijacker
}

func (w *hijackTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return commitOnHijack(w.trackingResponseWriter, w.hijacker)
}

// flushHijackTracker adds both http.Flusher and http.Hijacker to
// trackingResponseWriter, for an underlying writer that supports both -
// the common case for the stdlib HTTP/1.1 server's own ResponseWriter.
type flushHijackTracker struct {
	*trackingResponseWriter
	flusher  http.Flusher
	hijacker http.Hijacker
}

func (w *flushHijackTracker) Flush() { commitOnFlush(w.trackingResponseWriter, w.flusher) }

func (w *flushHijackTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return commitOnHijack(w.trackingResponseWriter, w.hijacker)
}

// flushErrorHijackTracker adds http.Flusher, FlushError() error, and
// http.Hijacker to trackingResponseWriter, for an underlying writer that
// reports flush failures and supports hijacking.
type flushErrorHijackTracker struct {
	*trackingResponseWriter
	flushErrorer flushErrorer
	hijacker     http.Hijacker
}

func (w *flushErrorHijackTracker) Flush() {
	_ = commitOnFlushError(w.trackingResponseWriter, w.flushErrorer)
}

func (w *flushErrorHijackTracker) FlushError() error {
	return commitOnFlushError(w.trackingResponseWriter, w.flushErrorer)
}

func (w *flushErrorHijackTracker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return commitOnHijack(w.trackingResponseWriter, w.hijacker)
}

// responseWriterUnwrapper is the interface http.ResponseController looks
// for to continue past a wrapper that doesn't itself implement the
// capability being searched for - documented by http.NewResponseController,
// not a named type in net/http. discoverHijacker/discoverFlusher below walk
// it the same way ResponseController does, so that capability discovery
// here can't be bypassed by unwrapping past this package's own tracker the
// way a direct type assertion could be (see discoverFlusher's doc comment).
type responseWriterUnwrapper interface {
	Unwrap() http.ResponseWriter
}

// discoverHijacker walks w's Unwrap chain - starting with w itself - the
// same way http.ResponseController's Hijack() does: check the current
// layer for http.Hijacker, and if it's not there, continue to
// Unwrap()'s result and try again. Returns nil if no layer implements it.
func discoverHijacker(w http.ResponseWriter) http.Hijacker {
	for {
		if hj, ok := w.(http.Hijacker); ok {
			return hj
		}
		u, ok := w.(responseWriterUnwrapper)
		if !ok {
			return nil
		}
		w = u.Unwrap()
	}
}

// discoverFlusher walks w's Unwrap chain the same way
// http.ResponseController's Flush() does: at each layer, prefer
// FlushError() error if present, else plain Flush() if present, else
// continue to the next layer via Unwrap(). Crucially this checks both
// interfaces at each layer before moving to the next, matching
// ResponseController's actual per-layer priority - a naive "search every
// layer for FlushError, then search every layer for Flusher" would give a
// deeper FlushError priority over a shallower plain Flusher that
// ResponseController would have stopped at first. Returns whichever it
// finds first (at most one of the two return values is non-nil).
func discoverFlusher(w http.ResponseWriter) (flushErrorer, http.Flusher) {
	for {
		if fe, ok := w.(flushErrorer); ok {
			return fe, nil
		}
		if f, ok := w.(http.Flusher); ok {
			return nil, f
		}
		u, ok := w.(responseWriterUnwrapper)
		if !ok {
			return nil, nil
		}
		w = u.Unwrap()
	}
}

// newTrackingResponseWriter wraps w for RecoveryMiddleware's commit
// tracking. It returns the http.ResponseWriter to pass to the handler -
// implementing http.Hijacker if and only if discoverHijacker finds one
// anywhere in w's Unwrap chain, and http.Flusher if and only if
// discoverFlusher finds a flush capability anywhere in that same chain:
// plain Flush(), FlushError() error, or both (FlushError is checked ahead
// of plain Flusher at each layer, matching http.ResponseController's own
// priority - see discoverFlusher). Searching the whole chain, not just w
// itself, matters because trackingResponseWriter.Unwrap() below
// unconditionally exposes the immediate w to http.ResponseController (for
// deadline-related operations) - if capability discovery here only checked
// w directly, a handler calling http.NewResponseController(wrapped).Flush()
// could unwrap straight through this tracker to a real flusher one or more
// layers down (behind an intermediate wrapper that implements only
// Unwrap()), flushing - and thereby committing - the response without this
// package ever finding out. Discovering the same capability
// ResponseController would discover, and exposing it on the returned
// wrapper itself, closes that gap: ResponseController checks the outermost
// writer's own methods before ever calling Unwrap(), so it now matches this
// wrapper immediately instead of reaching through it.
//
// One deliberate asymmetry follows from discoverFlusher: a writer whose
// only flush capability is FlushError() gains a plain Flush() method it
// didn't have, because the flush capability genuinely exists underneath and
// http.Flusher is how handlers conventionally probe for it - an adapter
// over a real capability, not a fabricated one (the FlushError method
// itself is also preserved, so no error information is lost). Nothing else
// is preserved: http.Pusher and io.ReaderFrom in particular are dropped by
// the wrapper, at any depth. The second return value is the
// *trackingResponseWriter base, for reading wroteHeader/status afterward
// regardless of which variant was returned (every variant embeds it by
// pointer, so its state is shared either way).
func newTrackingResponseWriter(w http.ResponseWriter) (http.ResponseWriter, *trackingResponseWriter) {
	base := &trackingResponseWriter{ResponseWriter: w}
	hijacker := discoverHijacker(w)
	flushErr, flusher := discoverFlusher(w)

	if flushErr != nil {
		if hijacker != nil {
			return &flushErrorHijackTracker{trackingResponseWriter: base, flushErrorer: flushErr, hijacker: hijacker}, base
		}
		return &flushErrorTracker{trackingResponseWriter: base, flushErrorer: flushErr}, base
	}

	switch {
	case flusher != nil && hijacker != nil:
		return &flushHijackTracker{trackingResponseWriter: base, flusher: flusher, hijacker: hijacker}, base
	case flusher != nil:
		return &flushTracker{trackingResponseWriter: base, flusher: flusher}, base
	case hijacker != nil:
		return &hijackTracker{trackingResponseWriter: base, hijacker: hijacker}, base
	default:
		return base, base
	}
}

// Middleware for error recovery and logging
func RecoveryMiddleware(logger Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped, tw := newTrackingResponseWriter(w)
			returnedNormally := false

			defer func() {
				rec := recover()

				// recover() also reports nil when next.ServeHTTP panicked
				// with a literal nil: whether that's true depends on the
				// panicnil GODEBUG default, which Go selects from the
				// *main* module's go directive, not this package's - so a
				// consumer whose own go.mod predates Go 1.21 gets the old
				// behavior regardless of what this module declares. It's
				// also what a bare recover() sees after next.ServeHTTP
				// calls runtime.Goexit. Neither can be told apart from the
				// other through recover() alone, but both are abnormal
				// exits, so returnedNormally - set only after ServeHTTP
				// actually returns - is what distinguishes them from a
				// genuinely uneventful request.
				if rec == nil && returnedNormally {
					return
				}

				if rec == http.ErrAbortHandler {
					// Conventionally used (including by net/http itself,
					// e.g. on client disconnect mid-response) to abort a
					// request without normal error handling. Let it
					// continue up the stack rather than logging it and
					// writing a response.
					panic(rec)
				}

				var err error
				switch v := rec.(type) {
				case error:
					err = WrapInternalError(v, "http_handler", "panic recovered")
				case string:
					err = NewInternalError("http_handler", "panic recovered")
				default:
					err = NewInternalError("http_handler", "unknown panic")
				}

				if tw.wroteHeader {
					// The handler already committed a response before
					// panicking - the status can't be changed at this
					// point, and writing another body would just corrupt
					// what was already sent. Log, then abort the
					// connection instead of returning normally: a plain
					// return here lets net/http treat whatever partial
					// bytes the handler managed to write as a complete,
					// successful response - the client sees a clean 200
					// with a truncated or invalid body and no transport-
					// level signal anything went wrong. http.ErrAbortHandler
					// is net/http's documented signal for exactly this -
					// it closes the HTTP/1 connection (or resets the
					// HTTP/2 stream) without an additional "http: panic
					// serving" log line, unlike a bare re-panic of rec.
					_, fields := errorLogFields(err, tw.status)
					fields["panic"] = rec
					fields["method"] = r.Method
					fields["path"] = r.URL.Path
					fields["response_committed_status"] = tw.status
					safeLog(logger, LevelError, err, fields, "Panic recovered in HTTP handler after response was already committed")
					panic(http.ErrAbortHandler)
				}

				// err here is always this package's own WrapInternalError/
				// NewInternalError, whose Details are always nil - it
				// can't produce a marshal failure the way a caller's own
				// SetPublicDetail could, so unlike WriteHTTPError there's
				// no render error worth plumbing through here.
				statusCode, _, writeErr := writeJSONErrorBody(tw, err)

				_, fields := errorLogFields(err, statusCode)
				fields["panic"] = rec
				fields["method"] = r.Method
				fields["path"] = r.URL.Path
				if writeErr != nil {
					fields["response_write_error"] = writeErr.Error()
				}
				safeLog(logger, LevelError, err, fields, "Panic recovered in HTTP handler")

				if writeErr != nil {
					// The replacement error body itself failed to fully
					// write (client disconnect, expired deadline, ...) -
					// the client may have received a partial, invalid
					// document that looks like a truncated success, the
					// same problem as the already-committed case above.
					// Abort rather than return normally.
					panic(http.ErrAbortHandler)
				}
			}()

			next.ServeHTTP(wrapped, r)
			returnedNormally = true
		})
	}
}
