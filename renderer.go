package svcerr

import (
	"fmt"
	"net/http"
)

// RendererConfig is the complete configuration for a Renderer - every
// knob the package-level globals expose, plus the logger, in one struct.
// The zero value reproduces the package's default behavior with no
// logging.
type RendererConfig struct {
	// StatusCodes maps application ErrorCodes to HTTP statuses, layered
	// over the built-in mapping (a built-in code may be overridden; an
	// absent code falls through to the built-ins). Each status must be
	// 400-599, the same rule RegisterStatusCode enforces. Note that a
	// Renderer does NOT consult the global RegisterStatusCode registry -
	// see Renderer.
	StatusCodes map[ErrorCode]int
	// DefaultAuthenticateChallenge is the WWW-Authenticate challenge for
	// 401 responses whose error doesn't provide its own via
	// SetAuthenticateChallenge - the per-instance equivalent of
	// SetDefaultAuthenticateChallenge. Empty means no default.
	DefaultAuthenticateChallenge string
	// HeaderPolicy configures representation-header handling for the
	// JSON/HTML/Problem methods - the per-instance equivalent of
	// SetHeaderPolicy.
	HeaderPolicy HeaderPolicy
	// RecoveryHeaderPolicy configures the same for Middleware's panic
	// replacement - the per-instance equivalent of
	// SetRecoveryHeaderPolicy. See SetRecoveryHeaderPolicy for why the
	// two paths are configured separately.
	RecoveryHeaderPolicy HeaderPolicy
	// Logger, when non-nil, receives one structured record per rendered
	// response from JSON/HTML/Problem (the same fields WriteHTTPError
	// logs) and per recovered panic from Middleware. Nil disables
	// logging; the methods still return their WriteResult either way, so
	// a Renderer never forces a choice between logging and result
	// reporting the way the package-level WriteHTTPError/WriteJSONResult
	// split does.
	Logger Logger
}

// Renderer renders error responses under a fixed, instance-scoped
// configuration - for tests, for processes hosting two differently
// configured services, or simply to avoid global state. Construct with
// NewRenderer; the configuration is copied and immutable from then on.
//
// A Renderer is fully self-contained: the package-level configuration -
// RegisterStatusCode's registry, SetDefaultAuthenticateChallenge,
// SetHeaderPolicy, SetRecoveryHeaderPolicy - does not affect it, and its
// own configuration never leaks to the package-level writers. If the
// application (or a library it uses) registers codes globally, mirror
// the ones this Renderer should know into RendererConfig.StatusCodes.
// Safe for concurrent use.
type Renderer struct {
	statusCodes    map[ErrorCode]int
	challenge      string
	policy         HeaderPolicy
	recoveryPolicy HeaderPolicy
	logger         Logger
}

// NewRenderer builds a Renderer from cfg. The config is deep-copied
// (mutating cfg or its StatusCodes map afterward has no effect), and
// every StatusCodes entry is validated against the same 400-599 rule as
// RegisterStatusCode - an invalid entry is rejected here, at startup,
// rather than surfacing later as a WriteHeader panic inside an error
// handler.
func NewRenderer(cfg RendererConfig) (*Renderer, error) {
	var codes map[ErrorCode]int
	if len(cfg.StatusCodes) > 0 {
		codes = make(map[ErrorCode]int, len(cfg.StatusCodes))
		for code, status := range cfg.StatusCodes {
			// See RegisterStatusCode: an empty key can never be reached
			// by this package's own errors (New/Wrap normalize it to
			// ErrCodeInternal), so it would only ever shadow
			// ErrCodeInternal's own entry for a caller-supplied Coder
			// that skips normalization.
			if code == "" {
				return nil, fmt.Errorf("svcerr: code must not be empty")
			}
			if status < 400 || status > 599 {
				return nil, fmt.Errorf("svcerr: status for %q must be 400-599, got %d", code, status)
			}
			codes[code] = status
		}
	}
	return &Renderer{
		statusCodes:    codes,
		challenge:      cfg.DefaultAuthenticateChallenge,
		policy:         cfg.HeaderPolicy,
		recoveryPolicy: cfg.RecoveryHeaderPolicy,
		logger:         cfg.Logger,
	}, nil
}

// status maps code through this Renderer's own StatusCodes, then the
// built-in mapping - never the global RegisterStatusCode registry.
func (r *Renderer) status(code ErrorCode) int {
	if status, ok := r.statusCodes[code]; ok {
		return status
	}
	return builtinStatusCode(code)
}

// settings is this Renderer's immutable snapshot for the normal error
// path - the instance counterpart of defaultRenderSettings.
func (r *Renderer) settings() renderSettings {
	return renderSettings{
		status:           r.status,
		defaultChallenge: r.challenge,
		policy:           r.policy,
	}
}

// recoverySettings mirrors settings for Middleware's panic replacement.
func (r *Renderer) recoverySettings() renderSettings {
	return renderSettings{
		status:           r.status,
		defaultChallenge: r.challenge,
		policy:           r.recoveryPolicy,
	}
}

// log emits the standard per-response record when a logger is
// configured. The nil check lives here (not just in safeLog) so a
// logger-less Renderer skips building log fields entirely - including
// the stack-trace resolution errorLogFields performs for 5xx responses.
func (r *Renderer) log(err error, statusCode int, renderErr, writeErr error, bytesWritten int) {
	if r.logger == nil {
		return
	}
	logError(r.logger, err, statusCode, renderErr, writeErr, bytesWritten)
}

// JSON renders err's standardized JSON error response under this
// Renderer's configuration - the same body WriteHTTPError writes. It
// both logs (when a Logger is configured) and returns the WriteResult,
// collapsing the package-level WriteHTTPError/WriteJSON/WriteJSONResult
// split into one method.
func (r *Renderer) JSON(w http.ResponseWriter, err error) WriteResult {
	statusCode, bytesWritten, renderErr, writeErr := writeJSONErrorBody(w, err, r.settings())
	r.log(err, statusCode, renderErr, writeErr, bytesWritten)
	return WriteResult{Status: statusCode, RenderErr: renderErr, WriteErr: writeErr, BytesWritten: bytesWritten}
}

// HTML mirrors JSON for the HTML fragment rendering WriteHTTPErrorHTML
// writes. RenderErr is always nil - see WriteResult.
func (r *Renderer) HTML(w http.ResponseWriter, err error) WriteResult {
	statusCode, bytesWritten, writeErr := writeHTMLErrorBody(w, err, r.settings())
	r.log(err, statusCode, nil, writeErr, bytesWritten)
	return WriteResult{Status: statusCode, WriteErr: writeErr, BytesWritten: bytesWritten}
}

// Problem mirrors JSON for the RFC 9457 application/problem+json
// rendering WriteHTTPProblem writes.
func (r *Renderer) Problem(w http.ResponseWriter, err error) WriteResult {
	statusCode, bytesWritten, renderErr, writeErr := writeProblemJSONBody(w, err, r.settings())
	r.log(err, statusCode, renderErr, writeErr, bytesWritten)
	return WriteResult{Status: statusCode, RenderErr: renderErr, WriteErr: writeErr, BytesWritten: bytesWritten}
}

// Middleware returns RecoveryMiddleware's behavior under this Renderer's
// configuration and Logger: panic recovery with commit tracking, writing
// the replacement internal-error response with this Renderer's status
// mapping, challenge default, and RecoveryHeaderPolicy.
func (r *Renderer) Middleware() func(http.Handler) http.Handler {
	return recoveryMiddleware(r.logger, r.recoverySettings)
}
