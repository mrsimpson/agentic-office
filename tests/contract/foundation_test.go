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

	artifactv1 "github.com/mrsimpson/agentic-office/hub/artifacts/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	journalv1 "github.com/mrsimpson/agentic-office/hub/journal/api"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// The office hub mounts every service of chapter 5 that crosses a process boundary. Each
// answers with its own result or error, not "not found" or "unimplemented". session_test.go,
// identity_test.go, journal_test.go and artifacts_test.go cover the services.
func TestServerMountsEveryInterface(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	c := srv.Admin(t)
	ctx := context.Background()

	if _, err := c.Session.Join(ctx, &sessionv1.JoinRequest{SessionId: "s", CommandId: "c"}); err != nil {
		t.Errorf("session commands: %v", err)
	}
	if _, err := c.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{SessionId: "s", CommandId: "c"}); err != nil {
		t.Errorf("auth API: %v", err)
	}
	if _, err := c.Journal.ListEntries(ctx, &journalv1.ListEntriesRequest{}); err != nil {
		t.Errorf("journal API: %v", err)
	}
	// The artifact service answers: the artifact does not exist yet.
	if _, err := c.Artifact.GetArtifact(ctx, &artifactv1.GetArtifactRequest{Id: "slides/1"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("artifact API: want code not_found, got %v", err)
	}
}

// Connect's own protocol is plain HTTP with JSON over TLS, so clients need no special library.
// The token goes into the Authorization header.
func TestPlainHTTPWithJSON(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
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

	status, body := post("/office.session.v1.SessionService/Join", "", `{"sessionId":"s","commandId":"c"}`)
	if err := json.Unmarshal(body, &e); err != nil || status != http.StatusUnauthorized || e.Code != "unauthenticated" {
		t.Fatalf("join without token: status %d, body %s", status, body)
	}

	token, err := srv.Admin(t).Tokens.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	status, body = post("/office.session.v1.SessionService/Join", token, `{"sessionId":"s","commandId":"c"}`)
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

	status, body = post("/office.journal.v1.JournalService/AppendEntry", token,
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

	status, body = post("/office.artifact.v1.ArtifactService/RenderSlides", token,
		`{"commandId":"c","title":"Plan","slides":[{"title":"One","bullets":["a"]}],"authorParticipantId":"admin"}`)
	var art struct {
		Artifact struct {
			ID        string `json:"id"`
			MediaType string `json:"mediaType"`
			Content   []byte `json:"content"` // base64 in JSON
		} `json:"artifact"`
	}
	if err := json.Unmarshal(body, &art); err != nil || status != http.StatusOK || art.Artifact.ID != "slides/1" ||
		art.Artifact.MediaType != "text/html; charset=utf-8" || !strings.Contains(string(art.Artifact.Content), "<h2>One</h2>") {
		t.Fatalf("render slides: status %d, body %s", status, body)
	}
	status, body = post("/office.artifact.v1.ArtifactService/GetArtifact", token, `{"id":"slides/2"}`)
	if err := json.Unmarshal(body, &e); err != nil || status != http.StatusNotFound || e.Code != "not_found" {
		t.Fatalf("get an unknown artifact: status %d, body %s", status, body)
	}
}

func TestServerStopsCleanly(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
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
