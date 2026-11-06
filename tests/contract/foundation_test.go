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
// The meeting session, identity and journal are implemented; session_test.go, identity_test.go and
// journal_test.go cover them.
func TestServerMountsEveryInterface(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	c := srv.Admin(t)
	ctx := context.Background()

	if _, err := c.Session.Join(ctx, &officev1.JoinRequest{SessionId: "s", CommandId: "c"}); err != nil {
		t.Errorf("session commands: %v", err)
	}
	if _, err := c.Identity.CreateStageLink(ctx, &officev1.CreateStageLinkRequest{SessionId: "s", CommandId: "c"}); err != nil {
		t.Errorf("auth API: %v", err)
	}
	if _, err := c.Journal.ListEntries(ctx, &officev1.ListEntriesRequest{}); err != nil {
		t.Errorf("journal API: %v", err)
	}
	calls := map[string]func() error{
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

// Connect's own protocol is plain HTTP with JSON over TLS, so clients need no special library.
// The token goes into the Authorization header.
func TestPlainHTTPWithJSON(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	hc := srv.HTTPClient(t)
	post := func(path, token, body string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	var e struct{ Code string }

	status, body := post("/office.v1.SessionService/Join", "", `{"sessionId":"s","commandId":"c"}`)
	if err := json.Unmarshal(body, &e); err != nil || status != http.StatusUnauthorized || e.Code != "unauthenticated" {
		t.Fatalf("join without token: status %d, body %s", status, body)
	}

	token, err := srv.Admin(t).Tokens.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	status, body = post("/office.v1.SessionService/Join", token, `{"sessionId":"s","commandId":"c"}`)
	var join struct {
		ParticipantID string `json:"participantId"`
		Result        struct {
			Event struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			} `json:"event"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &join); err != nil || status != http.StatusOK || join.ParticipantID != contracttest.AdminID ||
		join.Result.Event.ID != 1 || join.Result.Event.Type != "participant.joined" {
		t.Fatalf("join: status %d, body %s", status, body)
	}

	status, body = post("/office.v1.JournalService/AppendEntry", token,
		`{"commandId":"c","kind":"ENTRY_KIND_DECISION","text":"We ship.","authorParticipantId":"admin"}`)
	var entry struct {
		Entry struct {
			ID   int    `json:"id"`
			Kind string `json:"kind"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(body, &entry); err != nil || status != http.StatusOK || entry.Entry.ID != 1 ||
		entry.Entry.Kind != "ENTRY_KIND_DECISION" {
		t.Fatalf("journal append: status %d, body %s", status, body)
	}

	status, body = post("/office.v1.ArtifactService/GetArtifact", token, `{"id":"slides/1"}`)
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
