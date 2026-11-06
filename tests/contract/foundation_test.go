package contract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"
	"github.com/mrsimpson/agentic-office/core/contracttest"
)

// The office server mounts every service of chapter 5 that crosses a process boundary.
// Services without an implementation answer with "unimplemented", not "not found".
func TestServerMountsEveryInterface(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	c := contracttest.NewClients(srv.URL)
	ctx := context.Background()

	calls := map[string]func() error{
		"session commands": func() error {
			_, err := c.Session.Join(ctx, &officev1.JoinRequest{SessionId: "s", CommandId: "c"})
			return err
		},
		"session stream": func() error {
			st, err := c.Session.Subscribe(ctx, &officev1.SubscribeRequest{SessionId: "s"})
			if err != nil {
				return err
			}
			defer st.Close()
			_, err = st.Receive()
			return err
		},
		"auth API": func() error {
			_, err := c.Identity.GetChallenge(ctx, &officev1.GetChallengeRequest{})
			return err
		},
		"journal API": func() error {
			_, err := c.Journal.ListEntries(ctx, &officev1.ListEntriesRequest{})
			return err
		},
		"artifact API": func() error {
			_, err := c.Artifact.GetArtifact(ctx, &officev1.GetArtifactRequest{Id: "slides/1"})
			return err
		},
	}
	for name, call := range calls {
		if code := connect.CodeOf(call()); code != connect.CodeUnimplemented {
			t.Errorf("%s: want code unimplemented, got %v", name, code)
		}
	}
}

// Connect's own protocol is plain HTTP with JSON, so clients need no special library.
func TestPlainHTTPWithJSON(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	res, err := http.Post(srv.URL+"/office.v1.SessionService/Join", "application/json",
		strings.NewReader(`{"sessionId":"s","commandId":"c"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var e struct{ Code string }
	if err := json.Unmarshal(body, &e); err != nil || e.Code != "unimplemented" {
		t.Fatalf("status %d, body %s: want a Connect error with code unimplemented", res.StatusCode, body)
	}
}

func TestServerStopsCleanly(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	code, err := srv.Stop(5 * time.Second)
	if err != nil || code != 0 {
		t.Fatalf("exit code %d, err %v", code, err)
	}
}

func TestCLIVersionAndUsage(t *testing.T) {
	out, _, code := contracttest.Run(t, "office", nil, "version")
	if code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("office version: exit %d, stdout %q", code, out)
	}
	_, errOut, code := contracttest.Run(t, "office", nil, "no-such-command")
	if code != 2 || !strings.Contains(errOut, "usage:") {
		t.Fatalf("unknown command: exit %d, stderr %q", code, errOut)
	}
}
