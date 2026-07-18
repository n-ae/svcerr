package svcerr

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
)

// HTTPErrorResponse represents a standardized HTTP error response
type HTTPErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains detailed error information for API responses
type ErrorDetail struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// WriteResult reports what WriteJSONResult/WriteHTMLResult/
// WriteProblemResult actually did, for a caller that wants to detect a
// serialization or delivery failure without participating in this
// package's Logger contract. WriteHTTPError/WriteHTTPErrorHTML/
// WriteHTTPProblem carry the same information into their log fields
// (response_render_error, response_write_error, response_bytes_written,
// rendered_error_code) instead of returning it; the plain WriteJSON/
// WriteHTML/WriteProblem functions discard it entirely, same as before
// this type existed.
type WriteResult struct {
	// Status is the HTTP status code svcerr selected and passed to
	// w.WriteHeader - on a marshal failure, the active mapping's status
	// for ErrCodeInternal (500 by default), not necessarily err's own
	// classification. It's what svcerr chose to
	// send, not a transport confirmation that the client received exactly
	// that status: a custom or third-party ResponseWriter could ignore it,
	// have already committed a different status earlier, transform it, or
	// panic during WriteHeader (see trackingResponseWriter.WriteHeader).
	Status int
	// RenderErr is the marshal error when the real body couldn't be
	// JSON-encoded and a generic fallback was substituted instead (nil
	// otherwise) - including a caller-supplied json.Marshaler that
	// panicked, which is recovered and reported here as an error rather
	// than allowed to escape the response writer (see safeJSONMarshal).
	// Always nil from WriteHTMLResult, whose body is plain string
	// concatenation and can't fail to encode.
	RenderErr error
	// WriteErr is whatever the final w.Write returned (nil on a full
	// write) - a client disconnect, an expired deadline, or any other
	// transport failure during delivery. Unlike RenderErr, it doesn't
	// imply a different body was sent, only that delivery of the
	// intended one may have failed or been truncated.
	WriteErr error
	// BytesWritten is the number of body bytes the final w.Write reported
	// written: the full body length when WriteErr is nil, less on a
	// truncated delivery (WriteErr is then non-nil - the transport's own
	// error, or io.ErrShortWrite for a non-conforming writer that
	// under-reported with a nil error). Body accounting only; status line
	// and header bytes are never counted. Like Status, it's what the
	// ResponseWriter reported, not a transport confirmation the client
	// received those bytes.
	BytesWritten int
}

// WriteHTTPError writes a standardized error response to the HTTP
// response writer, logging through logger.
//
// Prefer a Renderer, whose JSON method both logs (via the config
// Logger) and returns the WriteResult in one call - this function
// remains fully supported, but new code shouldn't need the split
// logging/result variants it exists for.
func WriteHTTPError(w http.ResponseWriter, err error, logger Logger) {
	statusCode, bytesWritten, renderErr, writeErr := writeJSONErrorBody(w, err, defaultRenderSettings())
	logError(logger, err, statusCode, renderErr, writeErr, bytesWritten)
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
//
// Prefer a Renderer, whose JSON method both logs (via the config
// Logger) and returns the WriteResult in one call - this function
// remains fully supported, but new code shouldn't need the split
// logging/result variants it exists for.
func WriteJSONResult(w http.ResponseWriter, err error) WriteResult {
	statusCode, bytesWritten, renderErr, writeErr := writeJSONErrorBody(w, err, defaultRenderSettings())
	return WriteResult{Status: statusCode, RenderErr: renderErr, WriteErr: writeErr, BytesWritten: bytesWritten}
}

// checkedWrite writes body to w, returning an error if either Write itself
// failed or Write returned fewer bytes than len(body) with a nil error - a
// short write with no error violates io.Writer's documented contract
// ("Write must return a non-nil error if it returns n < len(p)"), which
// every real net/http-backed writer already honors. This guards
// specifically against a non-conforming custom writer, test double, or
// future adapter that violates that contract and would otherwise have a
// truncated body silently treated as a fully-delivered response.
func checkedWrite(w http.ResponseWriter, body []byte) (int, error) {
	n, err := w.Write(body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	return n, err
}

// safeJSONMarshal wraps json.Marshal with two additional guarantees
// json.Marshal alone doesn't make: a panic from a caller-supplied
// json.Marshaler becomes an error, and a nil error always means body is
// actually valid JSON.
//
// encoding/json recovers its own internal sentinel panics but
// deliberately re-panics any other value, including one raised inside a
// custom MarshalJSON - so a diagnostic value attached via
// SetPublicDetail could otherwise take down the very writer that exists
// to report failures. The same policy already governs the package's
// other caller-supplied collaborator: safeLog contains a panicking
// Logger for the same reason. When the panic value is itself an error,
// %w preserves it in the chain (errors.Is/As still reach it through
// RenderErr) instead of flattening it to text with %v. The recovered
// panic becomes a RenderErr and engages the standard marshal-failure
// fallback, degrading exactly like any other unencodable value instead
// of escaping the response path.
//
// json.Valid(body) after a nil error guards a narrower gap: under
// legacy panicnil semantics (GODEBUG=panicnil=1, or a consumer's own
// main module predating Go 1.21 - see recoveryMiddleware's doc comment
// for the same GODEBUG dependency), recover() returns nil for a literal
// panic(nil) inside a custom MarshalJSON, so the panic above goes
// undetected - but json.Marshal has already written a truncated,
// invalid document to its internal buffer and returns it with a nil
// error, because the encoder's own recovery only catches its own
// sentinel type. Without this check that broken body would be reported
// as a fully successful, fully written response. json.Valid is checked
// unconditionally, not only after a recovered panic, since any future
// encoder or wrapped Marshaler misbehaving the same way (returning
// malformed bytes with a nil error, panic or not) hits the identical
// invariant.
func safeJSONMarshal(v any) (body []byte, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			body = nil
			if recErr, ok := rec.(error); ok {
				err = fmt.Errorf("svcerr: JSON marshaler panicked: %w", recErr)
			} else {
				err = fmt.Errorf("svcerr: JSON marshaler panicked: %v", rec)
			}
		}
	}()

	body, err = json.Marshal(v)
	if err == nil && !json.Valid(body) {
		return nil, errors.New("svcerr: json.Marshal returned invalid JSON")
	}
	return body, err
}

// renderSettings bundles the configuration one response rendering uses:
// how a code maps to a status, the default WWW-Authenticate challenge,
// and the header policy. The package-level writers build it fresh per
// call from the mutable globals (RegisterStatusCode's registry,
// SetDefaultAuthenticateChallenge, SetHeaderPolicy/
// SetRecoveryHeaderPolicy), so startup-time reconfiguration keeps
// working; a Renderer passes an immutable snapshot of its own config
// instead, untouched by any of those globals.
type renderSettings struct {
	status           func(ErrorCode) int
	defaultChallenge string
	policy           HeaderPolicy
}

// defaultRenderSettings is the package-level writers' settings: the
// global registry-backed status mapping, the global challenge default,
// and the normal-path header policy, all read at call time.
func defaultRenderSettings() renderSettings {
	return renderSettings{
		status:           HTTPStatusCode,
		defaultChallenge: currentDefaultAuthChallenge(),
		policy:           currentHeaderPolicy(),
	}
}

// defaultRecoverySettings mirrors defaultRenderSettings for
// RecoveryMiddleware's panic replacement, which uses the separately
// configured recovery header policy.
func defaultRecoverySettings() renderSettings {
	return renderSettings{
		status:           HTTPStatusCode,
		defaultChallenge: currentDefaultAuthChallenge(),
		policy:           currentRecoveryHeaderPolicy(),
	}
}

// finalizeErrorResponse performs the delivery steps every body writer
// shares once the status and body bytes are decided: reset the
// representation headers per policy, restore the classification-specific
// headers, write the status, and deliver the body with short-write
// detection. A marshal-failure fallback passes node as nil - the
// response is a complete reclassification to ErrCodeInternal and no
// longer represents err's own classification, so no per-error header
// (Retry-After, an error-specific WWW-Authenticate challenge) may
// survive from it; both header helpers treat a nil node as carrying
// nothing. The default challenge still applies on a fallback whose
// mapped status is 401, exactly as it would for a bare internal error
// rendered under that mapping. This sequence exists once, here, because
// it demonstrably drifts when copied per format - the v0.6.4 HTML
// Retry-After omission was exactly such a copy missing one step.
func finalizeErrorResponse(w http.ResponseWriter, contentType string, s renderSettings, statusCode int, node coderError, body []byte) (bytesWritten int, writeErr error) {
	prepareErrorHeaders(w.Header(), contentType, s.policy)
	retryAfterHeader(w.Header(), node)
	setAuthenticateChallenge(w.Header(), statusCode, node, s.defaultChallenge)
	w.WriteHeader(statusCode)
	return checkedWrite(w, body)
}

// writeJSONErrorBody writes err's JSON body and headers to w and returns
// the status code used, without logging, plus the marshal error when the
// real body couldn't be encoded and a generic fallback was substituted
// instead (nil otherwise), and any error from the final w.Write (nil on a
// full write) - the caller decides what to do with either (log them, in
// WriteHTTPError's case). Split out of WriteHTTPError so RecoveryMiddleware
// can write the response and log the panic as a single record instead of
// the response write and the log call each logging independently.
func writeJSONErrorBody(w http.ResponseWriter, err error, s renderSettings) (statusCode, bytesWritten int, renderErr, writeErr error) {
	code := GetErrorCode(err)
	statusCode = s.status(code)
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
	body, marshalErr := safeJSONMarshal(errResp)
	if marshalErr != nil {
		// Complete reclassification to ErrCodeInternal: the status comes
		// from the active mapping for that code (a Renderer's StatusCodes
		// override or the RegisterStatusCode registry, not a hard-coded
		// 500), and node is dropped so no header derived from err's own,
		// no-longer-represented classification survives - see
		// finalizeErrorResponse.
		statusCode = s.status(ErrCodeInternal)
		body = fallbackErrorBody(ErrCodeInternal)
		renderErr = marshalErr
		node = nil
	}

	bytesWritten, writeErr = finalizeErrorResponse(w, "application/json", s, statusCode, node, body)

	return statusCode, bytesWritten, renderErr, writeErr
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

// WriteHTTPErrorHTML writes an HTML error response (for non-API
// endpoints), logging through logger.
//
// Prefer a Renderer, whose HTML method both logs (via the config
// Logger) and returns the WriteResult in one call - this function
// remains fully supported, but new code shouldn't need the split
// logging/result variants it exists for.
func WriteHTTPErrorHTML(w http.ResponseWriter, err error, logger Logger) {
	statusCode, bytesWritten, writeErr := writeHTMLErrorBody(w, err, defaultRenderSettings())
	logError(logger, err, statusCode, nil, writeErr, bytesWritten)
}

// WriteHTML mirrors WriteJSON for the HTML rendering WriteHTTPErrorHTML
// writes. Use WriteHTMLResult instead to also see a write failure this
// discards.
func WriteHTML(w http.ResponseWriter, err error) int {
	return WriteHTMLResult(w, err).Status
}

// WriteHTMLResult mirrors WriteJSONResult for the HTML rendering.
// RenderErr is always nil - see WriteResult.
//
// Prefer a Renderer, whose HTML method both logs (via the config
// Logger) and returns the WriteResult in one call - this function
// remains fully supported, but new code shouldn't need the split
// logging/result variants it exists for.
func WriteHTMLResult(w http.ResponseWriter, err error) WriteResult {
	statusCode, bytesWritten, writeErr := writeHTMLErrorBody(w, err, defaultRenderSettings())
	return WriteResult{Status: statusCode, WriteErr: writeErr, BytesWritten: bytesWritten}
}

// writeHTMLErrorBody mirrors writeJSONErrorBody for the HTML response.
func writeHTMLErrorBody(w http.ResponseWriter, err error, s renderSettings) (statusCode, bytesWritten int, writeErr error) {
	code := GetErrorCode(err)
	statusCode = s.status(code)
	message := getUserFriendlyMessage(code, err)
	node := outermostCoded(err)

	body := `<div class="error-message" role="alert">` +
		`<h3>Error</h3>` +
		`<p>` + html.EscapeString(message) + `</p>` +
		`</div>`

	// String concatenation can't fail to render, so HTML never has a
	// fallback body - node is always err's own classification here.
	bytesWritten, writeErr = finalizeErrorResponse(w, "text/html; charset=utf-8", s, statusCode, node, []byte(body))

	return statusCode, bytesWritten, writeErr
}

// ProblemDetails is the RFC 9457 (https://www.rfc-editor.org/rfc/rfc9457)
// "application/problem+json" response body written by WriteHTTPProblem.
// Code and any extractErrorDetails fields are extension members - RFC 9457
// says extension members live at the top level alongside the registered
// ones, which is what MarshalJSON does instead of nesting them.
type ProblemDetails struct {
	Type       string         // a URI reference identifying the problem type; "about:blank" when none is registered
	Title      string         // a short, occurrence-invariant summary of the problem type
	Status     int            // the HTTP status code for this occurrence
	Detail     string         // a human-readable explanation specific to this occurrence
	Instance   string         // a URI reference identifying this specific occurrence, if known
	Code       ErrorCode      // this package's own error code, as an extension member
	Extensions map[string]any // additional extension members (e.g. resource_id, field)
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
	out := make(map[string]any, len(p.Extensions)+5)
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
//
// Prefer a Renderer, whose Problem method both logs (via the config
// Logger) and returns the WriteResult in one call - this function
// remains fully supported, but new code shouldn't need the split
// logging/result variants it exists for.
func WriteHTTPProblem(w http.ResponseWriter, err error, logger Logger) {
	statusCode, bytesWritten, renderErr, writeErr := writeProblemJSONBody(w, err, defaultRenderSettings())
	logError(logger, err, statusCode, renderErr, writeErr, bytesWritten)
}

// WriteProblem mirrors WriteJSON for the RFC 9457 rendering
// WriteHTTPProblem writes. Use WriteProblemResult instead to also see a
// render/write failure this discards.
func WriteProblem(w http.ResponseWriter, err error) int {
	return WriteProblemResult(w, err).Status
}

// WriteProblemResult mirrors WriteJSONResult for the RFC 9457 rendering.
//
// Prefer a Renderer, whose Problem method both logs (via the config
// Logger) and returns the WriteResult in one call - this function
// remains fully supported, but new code shouldn't need the split
// logging/result variants it exists for.
func WriteProblemResult(w http.ResponseWriter, err error) WriteResult {
	statusCode, bytesWritten, renderErr, writeErr := writeProblemJSONBody(w, err, defaultRenderSettings())
	return WriteResult{Status: statusCode, RenderErr: renderErr, WriteErr: writeErr, BytesWritten: bytesWritten}
}

// writeProblemJSONBody mirrors writeJSONErrorBody for the problem+json body.
func writeProblemJSONBody(w http.ResponseWriter, err error, s renderSettings) (statusCode, bytesWritten int, renderErr, writeErr error) {
	code := GetErrorCode(err)
	statusCode = s.status(code)
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
	// status in general. http.StatusText returns "" for a status outside
	// its registered table - reachable here because a Renderer or
	// RegisterStatusCode may map any code to any 400-599 status,
	// including a nonstandard one (499, an application-specific 5xx,
	// ...) - so an empty result falls back to the same occurrence-
	// invariant per-code text the JSON/HTML bodies already use, rather
	// than shipping a blank RFC 9457 title.
	title := titleForStatus(statusCode, code)
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

	body, marshalErr := safeJSONMarshal(problem)
	if marshalErr != nil {
		// Complete reclassification to ErrCodeInternal, exactly as in
		// writeJSONErrorBody: status from the active mapping, node dropped.
		statusCode = s.status(ErrCodeInternal)
		body = fallbackProblemBody(statusCode)
		renderErr = marshalErr
		node = nil
	}

	bytesWritten, writeErr = finalizeErrorResponse(w, "application/problem+json", s, statusCode, node, body)

	return statusCode, bytesWritten, renderErr, writeErr
}

// fallbackProblemBody mirrors fallbackErrorBody for the problem+json body -
// built from fixed fields and titleForStatus, never from err or any
// caller-supplied detail value, so json.Marshal here cannot itself fail.
func fallbackProblemBody(statusCode int) []byte {
	body, _ := json.Marshal(ProblemDetails{
		Type:   "about:blank",
		Title:  titleForStatus(statusCode, ErrCodeInternal),
		Status: statusCode,
		Code:   ErrCodeInternal,
	})
	return body
}

// titleForStatus returns http.StatusText(statusCode), falling back to
// code's occurrence-invariant default message when the status has no
// registered reason phrase (e.g. a nonstandard status a Renderer or
// RegisterStatusCode mapped code to) - see writeProblemJSONBody's doc
// comment for why an empty title would otherwise be reachable.
func titleForStatus(statusCode int, code ErrorCode) string {
	if title := http.StatusText(statusCode); title != "" {
		return title
	}
	return defaultMessageForCode(code)
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
func extractErrorDetails(err error) map[string]any {
	details := make(map[string]any)
	node := outermostCoded(err)

	switch v := node.(type) {
	case *ValidationError:
		if v.field != "" {
			details["field"] = v.field
		}
		// v.value is deliberately not included here - it's whatever the
		// caller passed in (a password, a token, an oversized payload),
		// and this package has no way to know it's safe to publish.
	case *DatabaseError:
		if v.operation != "" {
			details["operation"] = v.operation
		}
	case *ExternalAPIError:
		details["service"] = v.service
		if v.statusCode > 0 {
			details["status_code"] = v.statusCode
		}
		if v.retryAfter != nil {
			// Also emitted as the Retry-After header - see retryAfterHeader.
			// Already clamped by SetRetryAfter, the only way to set it.
			details["retry_after"] = *v.retryAfter
		}
	case *NotFoundError:
		details["resource_type"] = v.resourceType
		if v.resourceID != "" {
			details["resource_id"] = v.resourceID
		}
	case *RateLimitError:
		details["limit"] = v.limit
		// Already clamped at construction, the only way to set it.
		details["retry_after"] = v.retryAfter
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
