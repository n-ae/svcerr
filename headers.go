package svcerr

import (
	"fmt"
	"net/http"
	"sync"
)

// HeaderPolicy configures how this package's response writers treat
// representation headers a handler (or a wrapping middleware) already set
// before the error body is written. The zero value is the long-standing
// default behavior: Content-Encoding is cleared, validators are kept.
// Field polarities follow from that - each false value must mean "what
// the package always did".
type HeaderPolicy struct {
	// KeepContentEncoding preserves a pre-existing Content-Encoding
	// header instead of deleting it. By default it's deleted because the
	// body these writers produce is always plain, uncompressed text - a
	// stale "gzip" left by a handler that never got to write its
	// compressed body would make clients gzip-decode plain JSON. Set this
	// when the ResponseWriter these writers receive belongs to a
	// transparent compression middleware that sets the header once and
	// compresses everything written through it: there the header is live,
	// not stale, and deleting it mislabels a genuinely compressed body as
	// plain text (see the README's compression-ordering section).
	KeepContentEncoding bool
	// ClearValidators additionally deletes ETag, Last-Modified, and
	// Accept-Ranges. By default they're kept: they describe the specific
	// successful representation this error response isn't attempting to
	// be, but - unlike a wrong Content-Length or Content-Encoding - a
	// stale conditional-request header doesn't actively mislead a client
	// about the body it's receiving, and a plain WriteJSON call may
	// legitimately want handler-set headers preserved. Set this for
	// deployments that would rather no abandoned-representation metadata
	// ever ride along on an error response.
	ClearValidators bool
}

var (
	headerPolicyMu sync.RWMutex
	// errorHeaderPolicy applies to the normal error path: WriteHTTPError/
	// WriteHTTPErrorHTML/WriteHTTPProblem and the WriteJSON/WriteHTML/
	// WriteProblem variants, i.e. an error body a handler writes on
	// purpose, usually into the exact ResponseWriter (compression wrappers
	// included) it would have written its success body into.
	errorHeaderPolicy HeaderPolicy
	// recoveryHeaderPolicy applies to RecoveryMiddleware's panic
	// replacement, which is written to the writer recovery itself wraps -
	// bypassing any middleware sitting between recovery and the handler.
	// The same deployment can genuinely need different answers for the
	// two paths: with a compression middleware inside recovery, a
	// handler's own WriteJSON goes through the compressor (its
	// Content-Encoding is live - keep it), while a panic replacement does
	// not (the same header is stale - clear it).
	recoveryHeaderPolicy HeaderPolicy
)

// SetHeaderPolicy sets the HeaderPolicy for the normal error path -
// WriteHTTPError/WriteHTTPErrorHTML/WriteHTTPProblem and the WriteJSON/
// WriteHTML/WriteProblem variants. It does not affect RecoveryMiddleware's
// panic replacement; see SetRecoveryHeaderPolicy. Set it once at startup,
// like RegisterStatusCode; the zero value restores the default behavior.
// Safe for concurrent use.
func SetHeaderPolicy(p HeaderPolicy) {
	headerPolicyMu.Lock()
	errorHeaderPolicy = p
	headerPolicyMu.Unlock()
}

// SetRecoveryHeaderPolicy sets the HeaderPolicy for RecoveryMiddleware's
// panic-replacement response. Separate from SetHeaderPolicy because the
// two paths write to different points in the middleware stack: a normal
// error body goes through whatever wraps the handler's ResponseWriter
// (e.g. a compression middleware, whose Content-Encoding is then live),
// while a panic replacement is written to the writer recovery wraps
// directly, underneath any such middleware (the same header is then
// stale). Set it once at startup; the zero value restores the default
// behavior. Safe for concurrent use.
func SetRecoveryHeaderPolicy(p HeaderPolicy) {
	headerPolicyMu.Lock()
	recoveryHeaderPolicy = p
	headerPolicyMu.Unlock()
}

// currentHeaderPolicy returns the policy for the normal error path.
func currentHeaderPolicy() HeaderPolicy {
	headerPolicyMu.RLock()
	defer headerPolicyMu.RUnlock()
	return errorHeaderPolicy
}

// currentRecoveryHeaderPolicy returns the policy for the panic path.
func currentRecoveryHeaderPolicy() HeaderPolicy {
	headerPolicyMu.RLock()
	defer headerPolicyMu.RUnlock()
	return recoveryHeaderPolicy
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
// has none. Content-Encoding is deleted (unless policy.KeepContentEncoding
// opts out, for transparent-compression deployments - see HeaderPolicy)
// because the body these writers produce is always plain, uncompressed
// text - a handler that set it while planning to write compressed bytes
// itself would otherwise leave clients trying to gzip-decode a body that
// was never actually compressed.
//
// Retry-After and WWW-Authenticate are deleted for the same reason as
// Content-Length: both describe the specific response this call is
// replacing, not necessarily this one - a handler (or a previous request
// through a reused/pooled ResponseWriter-like object) may have set either
// in anticipation of a response that never got written before this error
// took over. Every caller of prepareErrorHeaders re-adds Retry-After
// (retryAfterHeader) or WWW-Authenticate (setAuthenticateChallenge)
// immediately afterward when err's own classification actually calls for
// it, so a genuinely-applicable value is never lost. Neither deletion is
// policy-controlled: unlike Content-Encoding, there's no middleware
// topology in which a pre-existing value is live for this response.
//
// ETag, Last-Modified, and Accept-Ranges are kept by default - those
// describe a specific successful representation this response isn't
// attempting to be, but aren't actively misleading the way a wrong
// Content-Length or Content-Encoding is - unless policy.ClearValidators
// opts in to deleting them too.
func prepareErrorHeaders(h http.Header, contentType string, policy HeaderPolicy) {
	h.Del("Content-Length")
	if !policy.KeepContentEncoding {
		h.Del("Content-Encoding")
	}
	h.Del("Trailer")
	h.Del("Retry-After")
	h.Del("WWW-Authenticate")
	if policy.ClearValidators {
		h.Del("ETag")
		h.Del("Last-Modified")
		h.Del("Accept-Ranges")
	}
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
}

// retryAfterHeader sets Retry-After when node (the same outermost-coded
// node used for everything else in err's classification) carries a retry
// hint: a *RateLimitError's RetryAfter, or a *ExternalAPIError's non-nil
// RetryAfter (an upstream's own retry hint a gateway chose to record -
// RFC 9110 permits Retry-After on any response, and both types already
// expose the same value to clients as a retry_after details member, so
// the standard header is the strictly more useful spelling). Using the
// classification node means an outer wrapper's code can't inherit a
// wrapped error's header. Shared by all three response writers; skipped
// on the marshal-failure fallback by the JSON/problem+json callers, since
// that response no longer represents err's own classification.
//
// The stored retry values are trusted here without re-clamping: since
// v1 the fields are unexported and every entry point (the RateLimitError
// constructors, ExternalAPIError.SetRetryAfter) clamps to the
// non-negative delay-seconds RFC 9110 §10.2.3 requires, so validity is a
// real invariant of the stored value rather than something each emission
// site must re-establish.
func retryAfterHeader(h http.Header, node coderError) {
	switch v := node.(type) {
	case *RateLimitError:
		h.Set("Retry-After", fmt.Sprintf("%d", v.retryAfter))
	case *ExternalAPIError:
		if v.retryAfter != nil {
			h.Set("Retry-After", fmt.Sprintf("%d", *v.retryAfter))
		}
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
// Authenticator if it provides one, else defaultChallenge if non-empty
// (the SetDefaultAuthenticateChallenge value on the package-level render
// path, or RendererConfig.DefaultAuthenticateChallenge on a Renderer's),
// else nothing - RFC 9110 §11.6.1 requires at least one WWW-Authenticate
// challenge on every 401 response, but this package has no way to invent
// an application's authentication scheme or realm on its own, so both
// sources are opt-in. Shared by all three response writers (JSON, HTML,
// problem+json).
func setAuthenticateChallenge(h http.Header, statusCode int, node coderError, defaultChallenge string) {
	if statusCode != http.StatusUnauthorized {
		return
	}
	if a, ok := node.(Authenticator); ok {
		if challenge, set := a.AuthenticateChallenge(); set {
			h.Set("WWW-Authenticate", challenge)
			return
		}
	}
	if defaultChallenge != "" {
		h.Set("WWW-Authenticate", defaultChallenge)
	}
}

// currentDefaultAuthChallenge returns the application-wide challenge for
// the package-level render path; a Renderer carries its own instead.
func currentDefaultAuthChallenge() string {
	defaultAuthMu.RLock()
	defer defaultAuthMu.RUnlock()
	return defaultAuthChallenge
}
