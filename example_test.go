// Compiled, output-checked versions of the README's primary usage
// examples, in the external test package so they exercise only the
// exported API - `go test` fails if any drifts from the package's real
// behavior, and godoc renders them on the corresponding symbols. See
// contract_test.go for the broader external-package contract suite.
package svcerr_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/n-ae/svcerr"
)

// The README's primary flow: a service returns a semantic error, the
// handler renders it. WriteJSON is the logger-free variant of
// WriteHTTPError with the identical body.
func ExampleWriteJSON() {
	err := svcerr.NewNotFoundError("league", "12345")

	rec := httptest.NewRecorder()
	status := svcerr.WriteJSON(rec, err)

	fmt.Println(status)
	fmt.Println(rec.Body.String())
	// Output:
	// 404
	// {"error":{"code":"NOT_FOUND","message":"league not found: 12345","details":{"resource_id":"12345","resource_type":"league"}}}
}

// The RFC 9457 problem-details rendering of the same error: registered
// members plus this package's extension members, flattened to the top
// level.
func ExampleWriteProblem() {
	err := svcerr.NewNotFoundError("league", "12345")

	rec := httptest.NewRecorder()
	svcerr.WriteProblem(rec, err)

	fmt.Println(rec.Header().Get("Content-Type"))
	fmt.Println(rec.Body.String())
	// Output:
	// application/problem+json
	// {"code":"NOT_FOUND","detail":"league not found: 12345","resource_id":"12345","resource_type":"league","status":404,"title":"Not Found","type":"about:blank"}
}

// The HTML fragment rendering for HTMX-style endpoints. Rate-limit errors
// carry Retry-After on every rendering, HTML included.
func ExampleWriteHTML() {
	err := svcerr.NewRateLimitError("api", 100, 30)

	rec := httptest.NewRecorder()
	status := svcerr.WriteHTML(rec, err)

	fmt.Println(status, rec.Header().Get("Retry-After"))
	fmt.Println(rec.Body.String())
	// Output:
	// 429 30
	// <div class="error-message" role="alert"><h3>Error</h3><p>rate limit exceeded for api: 100 requests</p></div>
}

// Check error types with stdlib errors.As - there's no per-type IsXError
// wrapper.
func ExampleNewNotFoundError() {
	findLeague := func(id string) error {
		return svcerr.NewNotFoundError("league", id)
	}

	err := findLeague("12345")

	var nfErr *svcerr.NotFoundError
	if errors.As(err, &nfErr) {
		fmt.Println(nfErr.ResourceType, nfErr.ResourceID)
	}
	fmt.Println(svcerr.GetErrorCode(err), svcerr.HTTPStatusCode(svcerr.GetErrorCode(err)))
	// Output:
	// league 12345
	// NOT_FOUND 404
}

// Joined errors classify by their first coded child (stdlib errors.As
// traversal order), so reversing errors.Join's arguments would flip the
// response between 404 and 500. Classify a mixed-severity aggregate
// explicitly with Wrap instead of relying on argument order; the joined
// children stay reachable for errors.Is/errors.As.
func ExampleWrap() {
	notFound := svcerr.NewNotFoundError("user", "123")
	cleanupErr := errors.New("temp file cleanup failed")
	joined := errors.Join(notFound, cleanupErr)

	wrapped := svcerr.Wrap(joined, svcerr.ErrCodeInternal, "request processing failed")

	fmt.Println(svcerr.GetErrorCode(joined))
	fmt.Println(svcerr.GetErrorCode(wrapped))
	fmt.Println(errors.Is(wrapped, notFound), errors.Is(wrapped, cleanupErr))
	// Output:
	// NOT_FOUND
	// INTERNAL_ERROR
	// true true
}

// Operational categories never leak their own text to clients: UserMessage
// returns the sanitized message the response writers send, while Error()
// keeps the full detail for logs.
func ExampleUserMessage() {
	cause := errors.New("dial tcp 10.0.0.5:5432: connection refused")
	err := svcerr.WrapDatabaseError(cause, "query", "SELECT id FROM leagues")

	fmt.Println(svcerr.UserMessage(err))
	fmt.Println(err.Error())
	// Output:
	// Database error occurred. Please try again.
	// database query failed: dial tcp 10.0.0.5:5432: connection refused
}

// SetPublicMessage opts a specific error instance into a caller-chosen
// client-facing message, overriding the per-code default - the logged
// Error() text is unaffected.
func ExampleBaseError_SetPublicMessage() {
	err := svcerr.WrapDatabaseError(errors.New("connection refused"), "query", "SELECT 1")
	err.SetPublicMessage("We're having trouble reaching the database. Please try again shortly.")

	rec := httptest.NewRecorder()
	svcerr.WriteJSON(rec, err)

	fmt.Println(rec.Body.String())
	// Output:
	// {"error":{"code":"DB_QUERY","message":"We're having trouble reaching the database. Please try again shortly.","details":{"operation":"query"}}}
}

// An application-specific code outside the built-in set registers its
// HTTP status once, at startup. Custom codes aren't in the built-in
// safe-message categories, so pair them with SetPublicMessage.
func ExampleRegisterStatusCode() {
	const errCodeOutOfStock svcerr.ErrorCode = "OUT_OF_STOCK"
	if err := svcerr.RegisterStatusCode(errCodeOutOfStock, http.StatusConflict); err != nil {
		panic(err) // a bad registration should fail loudly at startup
	}

	err := svcerr.New(errCodeOutOfStock, "sku WIDGET-42 depleted, restock ETA unknown")
	err.SetPublicMessage("This item is out of stock.")

	rec := httptest.NewRecorder()
	status := svcerr.WriteJSON(rec, err)

	fmt.Println(status)
	fmt.Println(rec.Body.String())
	// Output:
	// 409
	// {"error":{"code":"OUT_OF_STOCK","message":"This item is out of stock."}}
}

// RecoveryMiddleware turns a handler panic into a proper 500 JSON
// response (when nothing was committed yet) and one structured log
// record; a nil logger disables logging without changing the response.
func ExampleRecoveryMiddleware() {
	handler := svcerr.RecoveryMiddleware(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("something broke")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/leagues", nil))

	fmt.Println(rec.Code)
	fmt.Println(rec.Body.String())
	// Output:
	// 500
	// {"error":{"code":"INTERNAL_ERROR","message":"An internal error occurred. Please contact support if the problem persists."}}
}
