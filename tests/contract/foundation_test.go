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
// The meeting session is implemented; session_test.go covers it.
func TestServerMountsEveryInterface(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	c := contracttest.NewClients(srv.URL)
	ctx := context.Background()

	if _, err := c.Session.Join(ctx, &officev1.JoinRequest{SessionId: "s", CommandId: "c", DisplayName: "Ada",
		Kind: officev1.ParticipantKind_PARTICIPANT_KIND_HUMAN}); err != nil {
		t.Errorf("session commands: %v", err)
	}
	calls := map[string]func() error{
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
	post := func(path, body string) (int, []byte) {
		t.Helper()
		res, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}

	status, body := post("/office.v1.SessionService/Join",
		`{"sessionId":"s","commandId":"c","displayName":"Ada","kind":"PARTICIPANT_KIND_HUMAN"}`)
	var join struct {
		ParticipantID string `json:"participantId"`
		Result        struct {
			Event struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			} `json:"event"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &join); err != nil || status != http.StatusOK || join.ParticipantID == "" ||
		join.Result.Event.ID != 1 || join.Result.Event.Type != "participant.joined" {
		t.Fatalf("join: status %d, body %s", status, body)
	}

	status, body = post("/office.v1.JournalService/ListEntries", `{}`)
	var e struct{ Code string }
	if err := json.Unmarshal(body, &e); err != nil || e.Code != "unimplemented" {
		t.Fatalf("status %d, body %s: want a Connect error with code unimplemented", status, body)
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
