// Package health is the health check of the hub (if-health): a plain address that a container
// platform or a load balancer asks to see whether the hub serves requests.
//
// Address:
//
//	GET /healthz   answers 200 and the body "ok"
//
// It needs no token and no cookie, reads no state (no archive, no identity data) and has no side
// effects. It answers on the same TLS port as the rest of the hub, so it also shows that the
// TLS handshake and the HTTP server work, not only that the port is open.
package health

import "net/http"

// Path is the address of the health check.
const Path = "/healthz"

// Body is what the health check answers.
const Body = "ok\n"

// Handler answers the health check. Mount it for GET (a GET pattern also serves HEAD).
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(Body))
	})
}
