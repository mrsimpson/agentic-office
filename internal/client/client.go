// Package client creates Connect clients for the office server.
package client

import (
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/mrsimpson/agentic-office/gen/office/v1/officev1connect"
)

// New returns a session client for baseURL, for example "http://127.0.0.1:8740".
// It uses the Connect protocol over HTTP/1.1, so server streams work without h2c.
func New(baseURL string) officev1connect.SessionServiceClient {
	hc := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	return officev1connect.NewSessionServiceClient(connect.NewClient(connecthttp.NewTransport(hc, baseURL)))
}
