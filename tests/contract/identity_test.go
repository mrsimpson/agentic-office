package contract

// Contract tests of Identity (bb-identity) through if-auth-api, and of the token check
// (if-token-check) as the session uses it. They start the real office-server and use only its
// interfaces and the shared client helpers in server/identity/api/auth.

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	identityv1 "github.com/mrsimpson/agentic-office/server/identity/api"
	"github.com/mrsimpson/agentic-office/server/identity/api/auth"
	journalv1 "github.com/mrsimpson/agentic-office/server/journal/api"
	sessionv1 "github.com/mrsimpson/agentic-office/server/session/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const (
	human = identityv1.ActorKind_ACTOR_KIND_HUMAN
	agent = identityv1.ActorKind_ACTOR_KIND_AGENT
)

func ctx10(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	k, err := auth.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// streamErr opens a stream and returns the error that ends it, or nil after a clean end. If first
// is set, it is called after the first event arrived, while the stream is open.
func streamErr(t *testing.T, c contracttest.Clients, sid string, first func()) error {
	t.Helper()
	st, err := c.Session.Subscribe(ctx10(t), &sessionv1.SubscribeRequest{SessionId: sid})
	if err != nil {
		return err
	}
	defer st.Close()
	if first != nil {
		if _, err := st.Receive(); err != nil {
			return err
		}
		first()
	}
	for {
		_, err := st.Receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func join(t *testing.T, c contracttest.Clients, sid string) *sessionv1.JoinResponse {
	t.Helper()
	res, err := c.Session.Join(ctx10(t), &sessionv1.JoinRequest{SessionId: sid, CommandId: ids.NewCommandID()})
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	return res
}

// A valid signature of a fresh challenge gives a token bound to the key. Everything else fails.
func TestIdentityChallengeAndResponse(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	anon := srv.Anonymous(t).Identity
	ctx := ctx10(t)
	key := newKey(t)
	pub := auth.PublicKey(key)

	_, err := anon.GetChallenge(ctx, &identityv1.GetChallengeRequest{PublicKey: pub})
	wantCode(t, "challenge for a key that is not registered", err, connect.CodeUnauthenticated)
	_, err = anon.GetChallenge(ctx, &identityv1.GetChallengeRequest{PublicKey: pub[:10]})
	wantCode(t, "challenge for a malformed key", err, connect.CodeInvalidArgument)

	if id := srv.RegisterKey(t, "ada", "Ada", human, key); id != auth.KeyID(pub) {
		t.Fatalf("key_id %q, want %q", id, auth.KeyID(pub))
	}
	challenge := func() []byte {
		t.Helper()
		res, err := anon.GetChallenge(ctx, &identityv1.GetChallengeRequest{PublicKey: pub})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(res.GetChallenge()), auth.ChallengePrefix) || !res.GetExpiresAt().AsTime().After(time.Now()) {
			t.Fatalf("challenge %q expires %v", res.GetChallenge(), res.GetExpiresAt().AsTime())
		}
		return res.GetChallenge()
	}
	exchange := func(pub ed25519.PublicKey, ch, sig []byte) (*identityv1.ExchangeChallengeResponse, error) {
		return anon.ExchangeChallenge(ctx, &identityv1.ExchangeChallengeRequest{PublicKey: pub, Challenge: ch, Signature: sig})
	}

	// Wrong signatures. Each failed exchange also uses up the challenge.
	other := newKey(t)
	ch := challenge()
	_, err = exchange(pub, ch, ed25519.Sign(other, ch))
	wantCode(t, "signature of another key", err, connect.CodeUnauthenticated)
	_, err = exchange(pub, ch, ed25519.Sign(key, ch))
	wantCode(t, "challenge used by a failed exchange", err, connect.CodeUnauthenticated)
	ch = challenge()
	tampered := append([]byte(nil), ch...)
	tampered[len(tampered)-1] ^= 1
	_, err = exchange(pub, tampered, ed25519.Sign(key, tampered))
	wantCode(t, "challenge that the server did not issue", err, connect.CodeUnauthenticated)
	sig := ed25519.Sign(key, ch)
	sig[0] ^= 1
	_, err = exchange(pub, ch, sig)
	wantCode(t, "broken signature", err, connect.CodeUnauthenticated)
	ch = challenge()
	_, err = exchange(auth.PublicKey(other), ch, ed25519.Sign(other, ch))
	wantCode(t, "challenge of another key", err, connect.CodeUnauthenticated)
	_, err = exchange(pub, nil, nil)
	wantCode(t, "exchange without challenge and signature", err, connect.CodeInvalidArgument)

	// The right signature.
	ch = challenge()
	sig, err = auth.SignChallenge(key, ch)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	res, err := exchange(pub, ch, sig)
	if err != nil {
		t.Fatal(err)
	}
	exp := res.GetExpiresAt().AsTime()
	if res.GetAccessToken() == "" || res.GetParticipantId() != "ada" ||
		exp.Before(before.Add(14*time.Minute)) || exp.After(before.Add(16*time.Minute)) {
		t.Fatalf("exchange: %v, want a token for ada that lives 15 minutes", res)
	}
	_, err = exchange(pub, ch, sig)
	wantCode(t, "challenge used twice", err, connect.CodeUnauthenticated)

	// The token works, and the session knows the participant from it.
	c := srv.WithToken(t, res.GetAccessToken())
	if j := join(t, c, "s"); j.GetParticipantId() != "ada" ||
		j.GetResult().GetEvent().GetParticipantJoined().GetDisplayName() != "Ada" {
		t.Fatalf("join with the token: %v", j)
	}
}

// The session rejects every command and the stream without a valid token.
func TestSessionRejectsCallsWithoutValidToken(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	join(t, ada.Clients, "s")

	for name, c := range map[string]contracttest.Clients{
		"no token":      srv.Anonymous(t),
		"unknown token": srv.WithToken(t, "oat_bm90IGEgdG9rZW4"),
		"empty bearer":  srv.WithToken(t, ""),
	} {
		ctx, cmd, s := ctx10(t), ids.NewCommandID, c.Session
		calls := map[string]func() error{
			"join": func() error {
				_, err := s.Join(ctx, &sessionv1.JoinRequest{SessionId: "s", CommandId: cmd()})
				return err
			},
			"leave": func() error {
				_, err := s.Leave(ctx, &sessionv1.LeaveRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada"})
				return err
			},
			"utterance": func() error {
				_, err := s.SubmitUtterance(ctx, &sessionv1.SubmitUtteranceRequest{SessionId: "s", CommandId: cmd(),
					ParticipantId: "ada", Text: "x", Modality: sessionv1.Modality_MODALITY_TEXT})
				return err
			},
			"retract": func() error {
				_, err := s.RetractUtterance(ctx, &sessionv1.RetractUtteranceRequest{SessionId: "s", CommandId: cmd(),
					ParticipantId: "ada", UtteranceEventId: 1})
				return err
			},
			"signal": func() error {
				_, err := s.SubmitSignal(ctx, &sessionv1.SubmitSignalRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada",
					Signal: &sessionv1.SubmitSignalRequest_TurnEnd{TurnEnd: &sessionv1.TurnEnd{ParticipantId: "ada"}}})
				return err
			},
			"pause": func() error {
				_, err := s.Pause(ctx, &sessionv1.PauseRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada"})
				return err
			},
			"resume": func() error {
				_, err := s.Resume(ctx, &sessionv1.ResumeRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada"})
				return err
			},
			"raise hand": func() error {
				_, err := s.RaiseHand(ctx, &sessionv1.RaiseHandRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada"})
				return err
			},
			"minutes": func() error {
				_, err := s.UpdateMinutes(ctx, &sessionv1.UpdateMinutesRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada"})
				return err
			},
			"show artifact": func() error {
				_, err := s.ShowArtifact(ctx, &sessionv1.ShowArtifactRequest{SessionId: "s", CommandId: cmd(),
					ParticipantId: "ada", ArtifactId: "slides/1"})
				return err
			},
			"end": func() error {
				_, err := s.EndSession(ctx, &sessionv1.EndSessionRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada"})
				return err
			},
			"subscribe": func() error { return streamErr(t, c, "s", nil) },
			"stage link": func() error {
				_, err := c.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{SessionId: "s", CommandId: cmd()})
				return err
			},
		}
		for call, do := range calls {
			wantCode(t, name+": "+call, do(), connect.CodeUnauthenticated)
		}
	}

	// Nothing was written: the log holds only the join of ada and the end.
	if log := endAndRead(t, ada, "s"); len(log) != 2 {
		t.Fatalf("log: %v, want the join and the end", log)
	}
}

// endAndRead ends a session as actor a and returns its whole log.
func endAndRead(t *testing.T, a *contracttest.Actor, sid string) []*sessionv1.Event {
	t.Helper()
	if _, err := a.Session.EndSession(ctx10(t), &sessionv1.EndSessionRequest{SessionId: sid, CommandId: ids.NewCommandID(),
		ParticipantId: a.ParticipantID}); err != nil {
		t.Fatal(err)
	}
	st, err := a.Session.Subscribe(ctx10(t), &sessionv1.SubscribeRequest{SessionId: sid})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var log []*sessionv1.Event
	for {
		res, err := st.Receive()
		if errors.Is(err, io.EOF) {
			return log
		}
		if err != nil {
			t.Fatal(err)
		}
		log = append(log, res.GetEvent())
	}
}

// Only admins register and revoke keys. A participant can have several keys.
func TestIdentityAdminRegistersKeys(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	admin := srv.Admin(t).Identity
	ctx := ctx10(t)
	key := newKey(t)
	reg := func(c contracttest.Clients, pid, name string, kind identityv1.ActorKind, k ed25519.PrivateKey) (string, error) {
		res, err := c.Identity.RegisterKey(ctx, &identityv1.RegisterKeyRequest{CommandId: ids.NewCommandID(),
			ParticipantId: pid, DisplayName: name, Kind: kind, PublicKey: auth.PublicKey(k)})
		return res.GetKeyId(), err
	}

	bot := srv.Participant(t, "bot", "Bot", agent)
	_, err := reg(bot.Clients, "eve", "Eve", human, key)
	wantCode(t, "register by a participant who is not an admin", err, connect.CodePermissionDenied)
	_, err = reg(srv.Anonymous(t), "eve", "Eve", human, key)
	wantCode(t, "register without a token", err, connect.CodeUnauthenticated)
	_, err = bot.Identity.RevokeKey(ctx, &identityv1.RevokeKeyRequest{CommandId: ids.NewCommandID(), KeyId: bot.KeyID})
	wantCode(t, "revoke by a participant who is not an admin", err, connect.CodePermissionDenied)

	adminClients := srv.Admin(t).Clients
	_, err = reg(adminClients, "", "Eve", human, key)
	wantCode(t, "register without participant_id", err, connect.CodeInvalidArgument)
	_, err = reg(adminClients, "eve smith", "Eve", human, key)
	wantCode(t, "register with a bad participant_id", err, connect.CodeInvalidArgument)
	_, err = reg(adminClients, "eve", "Eve", identityv1.ActorKind_ACTOR_KIND_UNSPECIFIED, key)
	wantCode(t, "register a new participant without kind", err, connect.CodeInvalidArgument)
	_, err = admin.RegisterKey(ctx, &identityv1.RegisterKeyRequest{CommandId: ids.NewCommandID(), ParticipantId: "eve",
		DisplayName: "Eve", Kind: human, PublicKey: []byte("short")})
	wantCode(t, "register a malformed key", err, connect.CodeInvalidArgument)

	req := &identityv1.RegisterKeyRequest{CommandId: ids.NewCommandID(), ParticipantId: "eve", DisplayName: "Eve",
		Kind: human, PublicKey: auth.PublicKey(key)}
	first, err := admin.RegisterKey(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := admin.RegisterKey(ctx, req)
	if err != nil || again.GetKeyId() != first.GetKeyId() {
		t.Fatalf("repeated register: %v, %v", again, err)
	}
	changed := &identityv1.RegisterKeyRequest{CommandId: req.GetCommandId(), ParticipantId: "eve", PublicKey: auth.PublicKey(newKey(t))}
	_, err = admin.RegisterKey(ctx, changed)
	wantCode(t, "command_id reused for another key", err, connect.CodeAlreadyExists)
	_, err = reg(adminClients, "bot", "", identityv1.ActorKind_ACTOR_KIND_UNSPECIFIED, key)
	wantCode(t, "register the key of eve for bot", err, connect.CodeAlreadyExists)
	_, err = reg(adminClients, "eve", "Eve", agent, newKey(t))
	wantCode(t, "register a key for eve with another kind", err, connect.CodeFailedPrecondition)

	// A second key of eve: both keys act as the same participant.
	second := newKey(t)
	if _, err := reg(adminClients, "eve", "", identityv1.ActorKind_ACTOR_KIND_UNSPECIFIED, second); err != nil {
		t.Fatal(err)
	}
	a, b := srv.WithKey(t, key), srv.WithKey(t, second)
	if a.ParticipantID != "eve" || b.ParticipantID != "eve" {
		t.Fatalf("participants of the two keys: %q and %q, want eve", a.ParticipantID, b.ParticipantID)
	}

	// Admin keys from the admin file are not revoked through the API.
	_, err = admin.RevokeKey(ctx, &identityv1.RevokeKeyRequest{CommandId: ids.NewCommandID(),
		KeyId: auth.KeyID(auth.PublicKey(srv.AdminKey))})
	wantCode(t, "revoke an admin key", err, connect.CodeFailedPrecondition)
	_, err = admin.RevokeKey(ctx, &identityv1.RevokeKeyRequest{CommandId: ids.NewCommandID(), KeyId: "SHA256:nope"})
	wantCode(t, "revoke an unknown key", err, connect.CodeNotFound)
}

// A removed key and its tokens stop working at once. Other keys of the participant still work.
func TestIdentityRevokedKey(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	admin := srv.Admin(t).Identity
	laptop := srv.Participant(t, "ada", "Ada", human)
	phone := srv.Participant(t, "ada", "", identityv1.ActorKind_ACTOR_KIND_UNSPECIFIED)
	join(t, laptop.Clients, "s")
	token, err := laptop.Tokens.Token(ctx10(t))
	if err != nil {
		t.Fatal(err)
	}
	fixed := srv.WithToken(t, token)

	// A stream of the laptop is open while the key is revoked.
	revoke := &identityv1.RevokeKeyRequest{CommandId: ids.NewCommandID(), KeyId: laptop.KeyID}
	streamEnd := streamErr(t, fixed, "s", func() {
		if _, err := admin.RevokeKey(ctx10(t), revoke); err != nil {
			t.Fatal(err)
		}
	})
	wantCode(t, "open stream of a revoked key", streamEnd, connect.CodeUnauthenticated)
	if _, err := admin.RevokeKey(ctx10(t), revoke); err != nil {
		t.Fatalf("repeated revoke: %v", err)
	}

	_, err = fixed.Session.SubmitUtterance(ctx10(t), &sessionv1.SubmitUtteranceRequest{SessionId: "s",
		CommandId: ids.NewCommandID(), ParticipantId: "ada", Text: "x", Modality: sessionv1.Modality_MODALITY_TEXT})
	wantCode(t, "token of a revoked key", err, connect.CodeUnauthenticated)
	_, err = srv.Anonymous(t).Identity.GetChallenge(ctx10(t), &identityv1.GetChallengeRequest{PublicKey: auth.PublicKey(laptop.Key)})
	wantCode(t, "challenge for a revoked key", err, connect.CodeUnauthenticated)
	_, err = admin.RegisterKey(ctx10(t), &identityv1.RegisterKeyRequest{CommandId: ids.NewCommandID(), ParticipantId: "ada",
		PublicKey: auth.PublicKey(laptop.Key)})
	wantCode(t, "register a revoked key again", err, connect.CodeFailedPrecondition)

	// The phone of ada still works.
	if _, err := phone.Session.SubmitUtterance(ctx10(t), &sessionv1.SubmitUtteranceRequest{SessionId: "s",
		CommandId: ids.NewCommandID(), ParticipantId: "ada", Text: "still here", Modality: sessionv1.Modality_MODALITY_TEXT}); err != nil {
		t.Fatalf("other key of the participant: %v", err)
	}
}

// Tokens expire after -token-ttl, also for open streams. TokenSource gets a new token by itself.
// Stage links expire after -stage-link-ttl.
func TestIdentityTokenExpires(t *testing.T) {
	const ttl = time.Second
	srv := contracttest.StartServer(t, nil, "-token-ttl", ttl.String(), "-stage-link-ttl", ttl.String())
	ada := srv.Participant(t, "ada", "Ada", human)
	join(t, ada.Clients, "s")
	token, err := ada.Tokens.Token(ctx10(t))
	if err != nil {
		t.Fatal(err)
	}
	fixed := srv.WithToken(t, token)
	link, err := ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: "s", CommandId: ids.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	stage := srv.WithToken(t, stageToken(t, link.GetUrl()))

	// Both streams are open when the tokens expire. Then they end with unauthenticated.
	start := time.Now()
	ends := make(chan error, 2)
	go func() { ends <- streamErr(t, fixed, "s", func() {}) }()
	go func() { ends <- streamErr(t, stage, "s", func() {}) }()
	for range 2 {
		wantCode(t, "open stream when the token expires", <-ends, connect.CodeUnauthenticated)
	}
	if d := time.Since(start); d > 3*ttl {
		t.Errorf("streams ended after %v, want about %v", d, ttl)
	}

	_, err = fixed.Session.RaiseHand(ctx10(t), &sessionv1.RaiseHandRequest{SessionId: "s", CommandId: ids.NewCommandID(), ParticipantId: "ada"})
	wantCode(t, "expired token", err, connect.CodeUnauthenticated)
	wantCode(t, "expired stage link", streamErr(t, stage, "s", nil), connect.CodeUnauthenticated)

	// The token source renews the token before it expires, so the actor keeps working.
	for range 3 {
		if _, err := ada.Session.RaiseHand(ctx10(t), &sessionv1.RaiseHandRequest{SessionId: "s",
			CommandId: ids.NewCommandID(), ParticipantId: "ada"}); err != nil {
			t.Fatalf("call with a renewed token: %v", err)
		}
		time.Sleep(ttl / 2)
	}
}

func stageToken(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	tok := u.Query().Get("token")
	if tok == "" {
		t.Fatalf("stage link %q has no token", link)
	}
	return tok
}

// A stage link is read-only and for one session: it can read the session stream and nothing else.
func TestStageLinkIsReadOnly(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	ctx := ctx10(t)
	sid := "team meeting/1"
	join(t, ada.Clients, sid)

	req := &identityv1.CreateStageLinkRequest{SessionId: sid, CommandId: ids.NewCommandID()}
	link, err := ada.Identity.CreateStageLink(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	want := srv.URL + "/stage/" + url.PathEscape(sid) + "?token="
	if !strings.HasPrefix(link.GetUrl(), want) || !link.GetExpiresAt().AsTime().After(time.Now().Add(23*time.Hour)) {
		t.Fatalf("stage link %v, want %s<token> that lives 24 hours", link, want)
	}
	again, err := ada.Identity.CreateStageLink(ctx, req)
	if err != nil || again.GetUrl() != link.GetUrl() {
		t.Fatalf("repeated stage link: %v, %v", again, err)
	}
	_, err = ada.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{CommandId: ids.NewCommandID()})
	wantCode(t, "stage link without session_id", err, connect.CodeInvalidArgument)

	stage := srv.WithToken(t, stageToken(t, link.GetUrl()))

	// It reads the stream of its session.
	got := make(chan *sessionv1.Event, 1)
	st, err := stage.Session.Subscribe(ctx, &sessionv1.SubscribeRequest{SessionId: sid})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer st.Close()
		if res, err := st.Receive(); err == nil {
			got <- res.GetEvent()
		}
		close(got)
	}()
	if ev := <-got; ev.GetType() != "participant.joined" || ev.GetParticipantJoined().GetParticipantId() != "ada" {
		t.Fatalf("stage stream: got %v, want the join of ada", ev)
	}
	wantCode(t, "stage link for another session", streamErr(t, stage, "other", nil), connect.CodePermissionDenied)

	// It cannot send commands or use other interfaces.
	cmd := ids.NewCommandID
	_, err = stage.Session.Join(ctx, &sessionv1.JoinRequest{SessionId: sid, CommandId: cmd()})
	wantCode(t, "join with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.Session.SubmitUtterance(ctx, &sessionv1.SubmitUtteranceRequest{SessionId: sid, CommandId: cmd(),
		ParticipantId: "ada", Text: "x", Modality: sessionv1.Modality_MODALITY_TEXT})
	wantCode(t, "utterance with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.Session.EndSession(ctx, &sessionv1.EndSessionRequest{SessionId: sid, CommandId: cmd(), ParticipantId: "ada"})
	wantCode(t, "end with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{SessionId: sid, CommandId: cmd()})
	wantCode(t, "stage link with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.Identity.RegisterKey(ctx, &identityv1.RegisterKeyRequest{CommandId: cmd(), ParticipantId: "x",
		DisplayName: "X", Kind: human, PublicKey: auth.PublicKey(newKey(t))})
	wantCode(t, "register a key with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.Journal.ListEntries(ctx, &journalv1.ListEntriesRequest{})
	wantCode(t, "journal with a stage link", err, connect.CodePermissionDenied)

	// Nothing was written.
	if log := endAndRead(t, ada, sid); len(log) != 2 {
		t.Fatalf("log: %v, want the join and the end", log)
	}
}

// The server speaks only TLS, and clients must trust its certificate.
func TestServerSpeaksOnlyTLS(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	plain := "http://" + strings.TrimPrefix(srv.URL, "https://")
	res, err := http.Post(plain+"/office.identity.v1.IdentityService/GetChallenge", "application/json", strings.NewReader("{}"))
	if err == nil {
		res.Body.Close()
		if res.StatusCode == http.StatusOK {
			t.Fatal("plain HTTP call succeeded")
		}
	}
	untrusted := contracttest.NewClients(srv.URL, nil)
	_, err = untrusted.Identity.GetChallenge(ctx10(t), &identityv1.GetChallengeRequest{PublicKey: auth.PublicKey(newKey(t))})
	if err == nil || connect.CodeOf(err) == connect.CodeUnauthenticated {
		t.Fatalf("client that does not trust the certificate: want a TLS error, got %v", err)
	}
}

// Registered keys, revocations and the certificate survive a restart of the server.
// Tokens do not: clients authenticate again.
func TestIdentitySurvivesRestart(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	gone := srv.Participant(t, "bot", "Bot", agent)
	if _, err := srv.Admin(t).Identity.RevokeKey(ctx10(t), &identityv1.RevokeKeyRequest{CommandId: ids.NewCommandID(),
		KeyId: gone.KeyID}); err != nil {
		t.Fatal(err)
	}
	old, err := ada.Tokens.Token(ctx10(t))
	if err != nil {
		t.Fatal(err)
	}
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}

	srv = contracttest.StartServerIn(t, srv.DataDir, srv.AdminKey, nil)
	_, err = srv.WithToken(t, old).Session.Join(ctx10(t), &sessionv1.JoinRequest{SessionId: "s", CommandId: ids.NewCommandID()})
	wantCode(t, "token from before the restart", err, connect.CodeUnauthenticated)
	if again := srv.WithKey(t, ada.Key); again.ParticipantID != "ada" {
		t.Fatalf("ada after the restart: %q", again.ParticipantID)
	}
	_, err = srv.Anonymous(t).Identity.GetChallenge(ctx10(t), &identityv1.GetChallengeRequest{PublicKey: auth.PublicKey(gone.Key)})
	wantCode(t, "revoked key after the restart", err, connect.CodeUnauthenticated)
}
