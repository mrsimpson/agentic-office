package contract

// Contract tests of Identity (bb-identity) through if-auth-api and if-user-api, and of the token check
// (if-token-check) as the session uses it. They start the real office-hub and use only its
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

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
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
	st, err := c.Stream.Subscribe(ctx10(t), &sessionstreamv1.SubscribeRequest{SessionId: sid})
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

func join(t *testing.T, c contracttest.Clients, sid string) *sessioncommandsv1.JoinResponse {
	t.Helper()
	res, err := c.Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: sid, CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	return res
}

// A valid signature of a fresh challenge gives a token bound to the key. Everything else fails.
func TestIdentityChallengeAndResponse(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
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
	srv.EnsureMember(t, "ada")
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
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	join(t, ada.Clients, "s")

	for name, c := range map[string]contracttest.Clients{
		"no token":      srv.Anonymous(t),
		"unknown token": srv.WithToken(t, "oat_bm90IGEgdG9rZW4"),
		"empty bearer":  srv.WithToken(t, ""),
	} {
		ctx, cmd, s := ctx10(t), coreapi.NewCommandID, c.Commands
		calls := map[string]func() error{
			"join": func() error {
				_, err := s.Join(ctx, &sessioncommandsv1.JoinRequest{SessionId: "s", CommandId: cmd()})
				return err
			},
			"leave": func() error {
				_, err := s.Leave(ctx, &sessioncommandsv1.LeaveRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada"})
				return err
			},
			"utterance": func() error {
				_, err := s.SubmitUtterance(ctx, &sessioncommandsv1.SubmitUtteranceRequest{SessionId: "s", CommandId: cmd(),
					ParticipantId: "ada", Text: "x", Modality: sessionstreamv1.Modality_MODALITY_TEXT})
				return err
			},
			"mark off the record": func() error {
				_, err := s.MarkOffTheRecord(ctx, &sessioncommandsv1.MarkOffTheRecordRequest{SessionId: "s", CommandId: cmd(),
					ParticipantId: "ada", UtteranceEventId: 1})
				return err
			},
			"signal": func() error {
				_, err := s.SubmitSignal(ctx, &sessioncommandsv1.SubmitSignalRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada",
					Signal: &sessioncommandsv1.SubmitSignalRequest_TurnEnd{TurnEnd: &sessionstreamv1.TurnEnd{ParticipantId: "ada"}}})
				return err
			},
			"pause": func() error {
				_, err := s.Pause(ctx, &sessioncommandsv1.PauseRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada"})
				return err
			},
			"resume": func() error {
				_, err := s.Resume(ctx, &sessioncommandsv1.ResumeRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada"})
				return err
			},
			"raise hand": func() error {
				_, err := s.RaiseHand(ctx, &sessioncommandsv1.RaiseHandRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada"})
				return err
			},
			"minutes": func() error {
				_, err := s.UpdateMinutes(ctx, &sessioncommandsv1.UpdateMinutesRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada"})
				return err
			},
			"show artifact": func() error {
				_, err := s.ShowArtifact(ctx, &sessioncommandsv1.ShowArtifactRequest{SessionId: "s", CommandId: cmd(),
					ParticipantId: "ada", ArtifactId: "slides/1"})
				return err
			},
			"end": func() error {
				_, err := s.EndSession(ctx, &sessioncommandsv1.EndSessionRequest{SessionId: "s", CommandId: cmd(), ParticipantId: "ada"})
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
func endAndRead(t *testing.T, a *contracttest.Actor, sid string) []*sessionstreamv1.Event {
	t.Helper()
	if _, err := a.Commands.EndSession(ctx10(t), &sessioncommandsv1.EndSessionRequest{SessionId: sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: a.ParticipantID}); err != nil {
		t.Fatal(err)
	}
	st, err := a.Stream.Subscribe(ctx10(t), &sessionstreamv1.SubscribeRequest{SessionId: sid})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var log []*sessionstreamv1.Event
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

// Only a system admin creates users. A user adds and revokes its own keys; a system admin any key.
func TestUserAPIKeys(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t).User
	ctx := ctx10(t)
	key := newKey(t)
	create := func(c contracttest.Clients, uid, name string, kind identityv1.ActorKind, k ed25519.PrivateKey) (string, error) {
		res, err := c.User.CreateUser(ctx, &userv1.CreateUserRequest{CommandId: coreapi.NewCommandID(),
			UserId: uid, DisplayName: name, Kind: kind, PublicKey: auth.PublicKey(k)})
		return res.GetKeyId(), err
	}

	bot := srv.Participant(t, "bot", "Bot", agent)
	_, err := create(bot.Clients, "eve", "Eve", human, key)
	wantCode(t, "create a user as a user who is no system admin", err, connect.CodePermissionDenied)
	_, err = create(srv.Anonymous(t), "eve", "Eve", human, key)
	wantCode(t, "create a user without a token", err, connect.CodeUnauthenticated)
	_, err = bot.User.ListUsers(ctx, &userv1.ListUsersRequest{})
	wantCode(t, "list users as a user who is no system admin", err, connect.CodePermissionDenied)

	adminClients := srv.Admin(t).Clients
	_, err = create(adminClients, "", "Eve", human, key)
	wantCode(t, "create without user_id", err, connect.CodeInvalidArgument)
	_, err = create(adminClients, "eve smith", "Eve", human, key)
	wantCode(t, "create with a bad user_id", err, connect.CodeInvalidArgument)
	_, err = create(adminClients, "eve", "Eve", identityv1.ActorKind_ACTOR_KIND_UNSPECIFIED, key)
	wantCode(t, "create without kind", err, connect.CodeInvalidArgument)
	_, err = admin.CreateUser(ctx, &userv1.CreateUserRequest{CommandId: coreapi.NewCommandID(), UserId: "eve",
		DisplayName: "Eve", Kind: human, PublicKey: []byte("short")})
	wantCode(t, "create with a malformed key", err, connect.CodeInvalidArgument)

	req := &userv1.CreateUserRequest{CommandId: coreapi.NewCommandID(), UserId: "eve", DisplayName: "Eve",
		Kind: human, PublicKey: auth.PublicKey(key)}
	first, err := admin.CreateUser(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := admin.CreateUser(ctx, req)
	if err != nil || again.GetKeyId() != first.GetKeyId() {
		t.Fatalf("repeated create: %v, %v", again, err)
	}
	changed := &userv1.CreateUserRequest{CommandId: req.GetCommandId(), UserId: "eve", DisplayName: "Eve",
		Kind: human, PublicKey: auth.PublicKey(newKey(t))}
	_, err = admin.CreateUser(ctx, changed)
	wantCode(t, "command_id reused for another key", err, connect.CodeAlreadyExists)
	_, err = create(adminClients, "eve", "Eve", human, newKey(t))
	wantCode(t, "create a user that exists", err, connect.CodeAlreadyExists)
	_, err = create(adminClients, "mallory", "Mallory", human, key)
	wantCode(t, "create a user with the key of eve", err, connect.CodeFailedPrecondition)

	// A second key of eve: both keys act as the same user. Eve adds it herself.
	second := newKey(t)
	eve := srv.WithKey(t, key)
	if _, err := eve.User.AddKey(ctx, &userv1.AddKeyRequest{CommandId: coreapi.NewCommandID(), UserId: "eve",
		PublicKey: auth.PublicKey(second)}); err != nil {
		t.Fatal(err)
	}
	if b := srv.WithKey(t, second); b.ParticipantID != "eve" {
		t.Fatalf("participant of the second key: %q, want eve", b.ParticipantID)
	}
	_, err = eve.User.AddKey(ctx, &userv1.AddKeyRequest{CommandId: coreapi.NewCommandID(), UserId: "bot",
		PublicKey: auth.PublicKey(newKey(t))})
	wantCode(t, "add a key for another user", err, connect.CodePermissionDenied)
	_, err = bot.User.AddKey(ctx, &userv1.AddKeyRequest{CommandId: coreapi.NewCommandID(), UserId: "bot",
		PublicKey: auth.PublicKey(key)})
	wantCode(t, "add the key of eve to bot", err, connect.CodeAlreadyExists)

	// Revoking: an own key yes, the key of another user no, an unknown key not found, the last
	// key of a user not (remove the user instead).
	_, err = eve.User.RevokeKey(ctx, &userv1.RevokeKeyRequest{CommandId: coreapi.NewCommandID(), KeyId: bot.KeyID})
	wantCode(t, "revoke the key of another user", err, connect.CodePermissionDenied)
	_, err = admin.RevokeKey(ctx, &userv1.RevokeKeyRequest{CommandId: coreapi.NewCommandID(), KeyId: "SHA256:nope"})
	wantCode(t, "revoke an unknown key", err, connect.CodeNotFound)
	_, err = admin.RevokeKey(ctx, &userv1.RevokeKeyRequest{CommandId: coreapi.NewCommandID(), KeyId: bot.KeyID})
	wantCode(t, "revoke the last key of a user", err, connect.CodeFailedPrecondition)
	if _, err := eve.User.RevokeKey(ctx, &userv1.RevokeKeyRequest{CommandId: coreapi.NewCommandID(),
		KeyId: auth.KeyID(auth.PublicKey(key))}); err != nil {
		t.Fatalf("eve revokes her first key: %v", err)
	}
}

// A removed key and its tokens stop working at once. Other keys of the participant still work.
func TestIdentityRevokedKey(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t).User
	laptop := srv.Participant(t, "ada", "Ada", human)
	phone := srv.Participant(t, "ada", "", identityv1.ActorKind_ACTOR_KIND_UNSPECIFIED)
	join(t, laptop.Clients, "s")
	token, err := laptop.Tokens.Token(ctx10(t))
	if err != nil {
		t.Fatal(err)
	}
	fixed := srv.WithToken(t, token)

	// A stream of the laptop is open while the key is revoked.
	revoke := &userv1.RevokeKeyRequest{CommandId: coreapi.NewCommandID(), KeyId: laptop.KeyID}
	streamEnd := streamErr(t, fixed, "s", func() {
		if _, err := admin.RevokeKey(ctx10(t), revoke); err != nil {
			t.Fatal(err)
		}
	})
	wantCode(t, "open stream of a revoked key", streamEnd, connect.CodeUnauthenticated)
	if _, err := admin.RevokeKey(ctx10(t), revoke); err != nil {
		t.Fatalf("repeated revoke: %v", err)
	}

	_, err = fixed.Commands.SubmitUtterance(ctx10(t), &sessioncommandsv1.SubmitUtteranceRequest{SessionId: "s",
		CommandId: coreapi.NewCommandID(), ParticipantId: "ada", Text: "x", Modality: sessionstreamv1.Modality_MODALITY_TEXT})
	wantCode(t, "token of a revoked key", err, connect.CodeUnauthenticated)
	_, err = srv.Anonymous(t).Identity.GetChallenge(ctx10(t), &identityv1.GetChallengeRequest{PublicKey: auth.PublicKey(laptop.Key)})
	wantCode(t, "challenge for a revoked key", err, connect.CodeUnauthenticated)
	_, err = admin.AddKey(ctx10(t), &userv1.AddKeyRequest{CommandId: coreapi.NewCommandID(), UserId: "ada",
		PublicKey: auth.PublicKey(laptop.Key)})
	wantCode(t, "add a revoked key again", err, connect.CodeFailedPrecondition)

	// The phone of ada still works.
	if _, err := phone.Commands.SubmitUtterance(ctx10(t), &sessioncommandsv1.SubmitUtteranceRequest{SessionId: "s",
		CommandId: coreapi.NewCommandID(), ParticipantId: "ada", Text: "still here", Modality: sessionstreamv1.Modality_MODALITY_TEXT}); err != nil {
		t.Fatalf("other key of the participant: %v", err)
	}
}

// Tokens expire after -token-ttl, also for open streams. TokenSource gets a new token by itself.
// Stage links expire after -stage-link-ttl.
func TestIdentityTokenExpires(t *testing.T) {
	const ttl = time.Second
	srv := contracttest.StartHub(t, nil, "-token-ttl", ttl.String(), "-stage-link-ttl", ttl.String())
	ada := srv.Participant(t, "ada", "Ada", human)
	join(t, ada.Clients, "s")
	token, err := ada.Tokens.Token(ctx10(t))
	if err != nil {
		t.Fatal(err)
	}
	fixed := srv.WithToken(t, token)
	link, err := ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: "s", CommandId: coreapi.NewCommandID()})
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

	_, err = fixed.Commands.RaiseHand(ctx10(t), &sessioncommandsv1.RaiseHandRequest{SessionId: "s", CommandId: coreapi.NewCommandID(), ParticipantId: "ada"})
	wantCode(t, "expired token", err, connect.CodeUnauthenticated)
	wantCode(t, "expired stage link", streamErr(t, stage, "s", nil), connect.CodeUnauthenticated)

	// The token source renews the token before it expires, so the actor keeps working.
	for range 3 {
		if _, err := ada.Commands.RaiseHand(ctx10(t), &sessioncommandsv1.RaiseHandRequest{SessionId: "s",
			CommandId: coreapi.NewCommandID(), ParticipantId: "ada"}); err != nil {
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
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	ctx := ctx10(t)
	sid := "team meeting/1"
	join(t, ada.Clients, sid)

	req := &identityv1.CreateStageLinkRequest{SessionId: sid, CommandId: coreapi.NewCommandID()}
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
	_, err = ada.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{CommandId: coreapi.NewCommandID()})
	wantCode(t, "stage link without session_id", err, connect.CodeInvalidArgument)

	stage := srv.WithToken(t, stageToken(t, link.GetUrl()))

	// It reads the stream of its session.
	got := make(chan *sessionstreamv1.Event, 1)
	st, err := stage.Stream.Subscribe(ctx, &sessionstreamv1.SubscribeRequest{SessionId: sid})
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
	cmd := coreapi.NewCommandID
	_, err = stage.Commands.Join(ctx, &sessioncommandsv1.JoinRequest{SessionId: sid, CommandId: cmd()})
	wantCode(t, "join with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.Commands.SubmitUtterance(ctx, &sessioncommandsv1.SubmitUtteranceRequest{SessionId: sid, CommandId: cmd(),
		ParticipantId: "ada", Text: "x", Modality: sessionstreamv1.Modality_MODALITY_TEXT})
	wantCode(t, "utterance with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.Commands.EndSession(ctx, &sessioncommandsv1.EndSessionRequest{SessionId: sid, CommandId: cmd(), ParticipantId: "ada"})
	wantCode(t, "end with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{SessionId: sid, CommandId: cmd()})
	wantCode(t, "stage link with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.User.CreateUser(ctx, &userv1.CreateUserRequest{CommandId: cmd(), UserId: "x",
		DisplayName: "X", Kind: human, PublicKey: auth.PublicKey(newKey(t))})
	wantCode(t, "create a user with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.Decision.ListDecisions(ctx, &decisionv1.ListDecisionsRequest{})
	wantCode(t, "decisions with a stage link", err, connect.CodePermissionDenied)

	// Nothing was written.
	if log := endAndRead(t, ada, sid); len(log) != 2 {
		t.Fatalf("log: %v, want the join and the end", log)
	}
}

// The server speaks only TLS, and clients must trust its certificate.
func TestServerSpeaksOnlyTLS(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
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
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	gone := srv.Participant(t, "bot", "Bot", agent)
	srv.Participant(t, "bot", "", agent) // a second key, so that the first one can be revoked
	if _, err := srv.Admin(t).User.RevokeKey(ctx10(t), &userv1.RevokeKeyRequest{CommandId: coreapi.NewCommandID(),
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

	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	_, err = srv.WithToken(t, old).Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: "s", CommandId: coreapi.NewCommandID()})
	wantCode(t, "token from before the restart", err, connect.CodeUnauthenticated)
	if again := srv.WithKey(t, ada.Key); again.ParticipantID != "ada" {
		t.Fatalf("ada after the restart: %q", again.ParticipantID)
	}
	_, err = srv.Anonymous(t).Identity.GetChallenge(ctx10(t), &identityv1.GetChallengeRequest{PublicKey: auth.PublicKey(gone.Key)})
	wantCode(t, "revoked key after the restart", err, connect.CodeUnauthenticated)
}
