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

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// The office hub mounts every service of chapter 5 that crosses a process boundary. Each
// answers with its own result or error, not "not found" or "unimplemented". session_test.go,
// identity_test.go, records_test.go, tasks_test.go and slides_test.go cover the services.
func TestServerMountsEveryInterface(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	c := srv.Admin(t)
	ctx := context.Background()

	if _, err := c.Commands.Join(ctx, &sessioncommandsv1.JoinRequest{SessionId: "s", CommandId: "c"}); err != nil {
		t.Errorf("session commands: %v", err)
	}
	if _, err := c.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{SessionId: "s", CommandId: "c"}); err != nil {
		t.Errorf("auth API: %v", err)
	}
	if _, err := c.Decision.ListDecisions(ctx, &decisionv1.ListDecisionsRequest{}); err != nil {
		t.Errorf("decisions: %v", err)
	}
	if _, err := c.Note.ListNotes(ctx, &notev1.ListNotesRequest{}); err != nil {
		t.Errorf("notes: %v", err)
	}
	// The minutes answer: the session has no stored minutes.
	if _, err := c.Minutes.GetMinutes(ctx, &minutesv1.GetMinutesRequest{SessionId: "s"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("minutes: want code not_found, got %v", err)
	}
	// The slides interface answers: the deck does not exist yet.
	if _, err := c.Slides.GetDeck(ctx, &slidesv1.GetDeckRequest{Id: "slides/1"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("slides: want code not_found, got %v", err)
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

	status, body := post("/office.sessioncommands.v1.SessionCommandsService/Join", "", `{"sessionId":"s","commandId":"c"}`)
	if err := json.Unmarshal(body, &e); err != nil || status != http.StatusUnauthorized || e.Code != "unauthenticated" {
		t.Fatalf("join without token: status %d, body %s", status, body)
	}

	token, err := srv.Admin(t).Tokens.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	status, body = post("/office.sessioncommands.v1.SessionCommandsService/Join", token, `{"sessionId":"s","commandId":"c"}`)
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

	status, body = post("/office.decision.v1.DecisionService/RecordDecision", token,
		`{"commandId":"c","text":"We ship.","authorParticipantId":"admin"}`)
	var dec struct {
		Decision struct {
			ID     int    `json:"id"`
			Author string `json:"authorParticipantId"`
		} `json:"decision"`
	}
	if err := json.Unmarshal(body, &dec); err != nil || status != http.StatusOK || dec.Decision.ID != 1 ||
		dec.Decision.Author != contracttest.AdminID {
		t.Fatalf("record a decision: status %d, body %s", status, body)
	}

	status, body = post("/office.slides.v1.SlidesService/RenderDeck", token,
		`{"commandId":"c","title":"Plan","slides":[{"title":"One","bullets":["a"]}],"authorParticipantId":"admin"}`)
	var deck struct {
		Deck struct {
			ID        string `json:"id"`
			MediaType string `json:"mediaType"`
			Content   []byte `json:"content"` // base64 in JSON
		} `json:"deck"`
	}
	if err := json.Unmarshal(body, &deck); err != nil || status != http.StatusOK || deck.Deck.ID != "slides/1" ||
		deck.Deck.MediaType != "text/html; charset=utf-8" || !strings.Contains(string(deck.Deck.Content), "<h2>One</h2>") {
		t.Fatalf("render a deck: status %d, body %s", status, body)
	}
	status, body = post("/office.slides.v1.SlidesService/GetDeck", token, `{"id":"slides/2"}`)
	if err := json.Unmarshal(body, &e); err != nil || status != http.StatusNotFound || e.Code != "not_found" {
		t.Fatalf("get an unknown deck: status %d, body %s", status, body)
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
