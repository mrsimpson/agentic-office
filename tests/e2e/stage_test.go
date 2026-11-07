//go:build e2e

package e2e

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// stage is the raw event stream of a stage page, as a browser receives it.
type stage struct {
	mu  sync.Mutex
	raw strings.Builder
}

// openStage loads the stage page of a link and opens its event stream.
func openStage(t *testing.T, srv *contracttest.Server, link string) *stage {
	t.Helper()
	hc := srv.HTTPClient(t)
	res, err := hc.Get(link)
	if err != nil {
		t.Fatalf("stage page: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("stage page %s: status %d", link, res.StatusCode)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	u.RawPath = u.EscapedPath() + "/events"
	u.Path += "/events"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	res, err = hc.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("stage events: %v, %v", res, err)
	}
	s := &stage{}
	go func() {
		defer res.Body.Close()
		buf := make([]byte, 32*1024)
		for {
			n, err := res.Body.Read(buf)
			s.mu.Lock()
			s.raw.Write(buf[:n])
			s.mu.Unlock()
			if err == io.EOF || err != nil {
				return
			}
		}
	}()
	return s
}

// waitFor waits until the stream contains every string of want.
func (s *stage) waitFor(t *testing.T, what string, want ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.mu.Lock()
		raw := s.raw.String()
		s.mu.Unlock()
		missing := ""
		for _, w := range want {
			if !strings.Contains(raw, w) {
				missing = w
				break
			}
		}
		if missing == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stage: no %s: %q is missing in the stream", what, missing)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
