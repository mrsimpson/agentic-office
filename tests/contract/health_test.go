package contract

// Contract tests of the health check (if-health). They start the real office-hub and call the
// address like a container probe does: unauthenticated, over TLS. The address is part of the
// contract, so the tests spell it out.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const healthPath = "/healthz"

// A probe gets 200 and "ok" without a token or a cookie, and no cookie is set.
func TestHealthAnswersWithoutLogin(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	res, body := fetch(t, srv.HTTPClient(t), srv.URL+healthPath)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", healthPath, res.StatusCode)
	}
	if strings.TrimSpace(body) != "ok" {
		t.Fatalf("GET %s: body %q, want ok", healthPath, body)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content type %q, want text/plain", ct)
	}
	if len(res.Cookies()) != 0 {
		t.Fatalf("the health check sets a cookie: %v", res.Cookies())
	}
}

// HEAD works too (some load balancers use it); other methods are refused.
func TestHealthAcceptsOnlyReadMethods(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	hc := srv.HTTPClient(t)
	head, err := hc.Head(srv.URL + healthPath)
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != http.StatusOK {
		t.Fatalf("HEAD: status %d, want 200", head.StatusCode)
	}
	post, err := hc.Post(srv.URL+healthPath, "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, post.Body)
	post.Body.Close()
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST: status %d, want 405", post.StatusCode)
	}
}

// The health check changes no other address: the root and the stage stay 404 for an anonymous GET.
func TestHealthLeavesOtherAddressesAlone(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	hc := srv.HTTPClient(t)
	for _, p := range []string{"/", "/stage/"} {
		res, _ := fetch(t, hc, srv.URL+p)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", p, res.StatusCode)
		}
	}
}
