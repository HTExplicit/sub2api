package service

import "net/http"

// Prepared forwarding restores the caller's context after qualification. Its
// immutable application proof survives that restoration; the transient Apply
// marker does not. Compare the proof against the cookie at the actual boundary.
func codexBorrowHTTPApplied(req *http.Request) bool {
	if req == nil {
		return false
	}
	cookie, err := req.Cookie("__oailb")
	if err != nil || cookie.Value == "" {
		return false
	}
	if prepared, ok := req.Context().Value(codexGatewayBorrowHTTPPreparationContextKey{}).(codexGatewayBorrowHTTPPreparation); ok {
		application := prepared.application
		return application != nil && application.Applied && application.CookieFingerprint == borrowHash(cookie.Value)
	}
	return req.Context().Value(codexBorrowAppliedContextKey{}) == true
}
