// Package cors implements CORS preflight handling and middleware used by
// the connector. The original TypeScript server used a wildcard allow-list
// to support Cursor's browser-based testing, and the Go version mirrors
// that behaviour to preserve compatibility with existing deployments.
package cors

import "net/http"

// AllowedHeaders is the value sent in Access-Control-Allow-Headers for
// preflight requests. "*" matches every header name.
const AllowedHeaders = "*"

// AllowedMethods is the value sent in Access-Control-Allow-Methods.
const AllowedMethods = "GET, POST, PUT, DELETE, PATCH, OPTIONS, HEAD"

// Preflight writes a 204 No Content response with permissive CORS headers.
func Preflight(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", AllowedMethods)
	h.Set("Access-Control-Allow-Headers", AllowedHeaders)
	h.Set("Access-Control-Allow-Credentials", "true")
	h.Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}

// Middleware wraps an http.Handler and adds CORS headers to every response.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			Preflight(w)
			return
		}
		next.ServeHTTP(w, r)
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", AllowedMethods)
		h.Set("Access-Control-Allow-Headers", AllowedHeaders)
		h.Set("Access-Control-Allow-Credentials", "true")
	})
}
