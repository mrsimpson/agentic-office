package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"
	"github.com/mrsimpson/agentic-office/core/api/office/v1/officev1connect"
	"github.com/mrsimpson/agentic-office/core/auth"
)

// Options configure a Service.
type Options struct {
	// TokenTTL is the lifetime of an access token. Default 15 minutes (concept-identity).
	TokenTTL time.Duration
	// ChallengeTTL is how long a challenge can be exchanged. Default 1 minute.
	ChallengeTTL time.Duration
	// StageLinkTTL is the lifetime of a stage link. Default 24 hours.
	StageLinkTTL time.Duration
	// PublicURL is the base URL of the server in stage links, for example
	// "https://office.example.org:8740".
	PublicURL string
	// StateFile stores participants and keys. Empty means memory only.
	StateFile string
	// Admins are the admin keys, usually from LoadAdmins.
	Admins []Admin
}

// Caller is who makes a call, as the token check learned it from the token.
type Caller struct {
	ParticipantID string
	DisplayName   string
	Kind          officev1.ParticipantKind
	KeyID         string
	Admin         bool
	// StageSessionID is set for the token of a stage link. Such a token may only read the
	// session stream of this session.
	StageSessionID string
	// Expires is when the token expires.
	Expires time.Time
	// Done is closed when the token expires or its key is revoked.
	Done <-chan struct{}
}

type callerKey struct{}

// CallerFrom returns the caller that the Interceptor found for this call.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

type participant struct {
	DisplayName string                   `json:"display_name"`
	Kind        officev1.ParticipantKind `json:"kind"`
}

type key struct {
	ParticipantID string            `json:"participant_id"`
	PublicKey     ed25519.PublicKey `json:"public_key"`
	Revoked       bool              `json:"revoked,omitempty"`
	admin         bool              // from the admin file, not stored
}

type challenge struct {
	keyID   string
	expires time.Time
}

type token struct {
	keyID        string
	stageSession string
	expires      time.Time
	done         chan struct{}
	timer        *time.Timer
}

func (t *token) end() {
	t.timer.Stop()
	select {
	case <-t.done:
	default:
		close(t.done)
	}
}

type command struct {
	caller string
	req    proto.Message
	res    proto.Message
}

// Service implements IdentityService (if-auth-api) and the token check (if-token-check).
type Service struct {
	opts Options

	mu           sync.Mutex
	participants map[string]*participant
	keys         map[string]*key // by key ID
	challenges   map[string]challenge
	tokens       map[[32]byte]*token // by SHA-256 of the token
	commands     map[string]command
}

var _ officev1connect.IdentityServiceHandler = (*Service)(nil)

// New returns a Service. It reads the state file if there is one.
func New(opts Options) (*Service, error) {
	if opts.TokenTTL <= 0 {
		opts.TokenTTL = 15 * time.Minute
	}
	if opts.ChallengeTTL <= 0 {
		opts.ChallengeTTL = time.Minute
	}
	if opts.StageLinkTTL <= 0 {
		opts.StageLinkTTL = 24 * time.Hour
	}
	opts.PublicURL = strings.TrimSuffix(opts.PublicURL, "/")
	s := &Service{opts: opts, participants: map[string]*participant{}, keys: map[string]*key{},
		challenges: map[string]challenge{}, tokens: map[[32]byte]*token{}, commands: map[string]command{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	for _, a := range opts.Admins {
		if err := s.addAdmin(a); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func invalid(msg string) error         { return connect.NewError(connect.CodeInvalidArgument, msg) }
func unauthenticated(msg string) error { return connect.NewError(connect.CodeUnauthenticated, msg) }
func denied(msg string) error          { return connect.NewError(connect.CodePermissionDenied, msg) }
func precondition(msg string) error    { return connect.NewError(connect.CodeFailedPrecondition, msg) }

// publicProcedures need no token.
var publicProcedures = map[string]bool{
	officev1connect.IdentityServiceGetChallengeProcedure:      true,
	officev1connect.IdentityServiceExchangeChallengeProcedure: true,
}

// Interceptor is the token check for every call to the server. It reads the token from the
// header "Authorization: Bearer <token>". Calls without a valid token fail with Unauthenticated,
// except GetChallenge and ExchangeChallenge. A stage link token may only call
// SessionService/Subscribe. The caller is available to the handlers through CallerFrom.
func (s *Service) Interceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if publicProcedures[spec.Procedure] {
				return next(ctx, spec, stream)
			}
			var tok string
			if info, ok := connect.CallInfoForServerContext(ctx); ok {
				tok = auth.TokenFromHeader(info.RequestHeader().Get(auth.AuthorizationHeader))
			}
			if tok == "" {
				return unauthenticated("access token missing: send the header Authorization: Bearer <token>")
			}
			c, err := s.CheckToken(tok)
			if err != nil {
				return err
			}
			if c.StageSessionID != "" && spec.Procedure != officev1connect.SessionServiceSubscribeProcedure {
				return denied("a stage link only allows reading the session stream")
			}
			return next(context.WithValue(ctx, callerKey{}, c), spec, stream)
		}
	}
}

// CheckToken returns the caller a token belongs to (if-token-check). An unknown, expired or
// revoked token fails with Unauthenticated.
func (s *Service) CheckToken(tok string) (Caller, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := sha256.Sum256([]byte(tok))
	t := s.tokens[h]
	if t == nil {
		return Caller{}, unauthenticated("access token unknown or revoked: authenticate again")
	}
	if !time.Now().Before(t.expires) {
		t.end()
		delete(s.tokens, h)
		return Caller{}, unauthenticated("access token expired: authenticate again")
	}
	if t.stageSession != "" {
		return Caller{StageSessionID: t.stageSession, Expires: t.expires, Done: t.done}, nil
	}
	k := s.keys[t.keyID]
	if k == nil || k.Revoked {
		return Caller{}, unauthenticated("key revoked")
	}
	p := s.participants[k.ParticipantID]
	return Caller{ParticipantID: k.ParticipantID, DisplayName: p.DisplayName, Kind: p.Kind, KeyID: t.keyID,
		Admin: k.admin, Expires: t.expires, Done: t.done}, nil
}

// newToken creates a token. The caller holds s.mu.
func (s *Service) newToken(prefix, keyID, stageSession string, ttl time.Duration) (string, time.Time) {
	now := time.Now()
	for h, t := range s.tokens {
		if !now.Before(t.expires) {
			t.end()
			delete(s.tokens, h)
		}
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok := prefix + base64.RawURLEncoding.EncodeToString(b)
	t := &token{keyID: keyID, stageSession: stageSession, expires: now.Add(ttl), done: make(chan struct{})}
	t.timer = time.AfterFunc(ttl, t.end)
	s.tokens[sha256.Sum256([]byte(tok))] = t
	return tok, t.expires
}

func (s *Service) GetChallenge(_ context.Context, req *officev1.GetChallengeRequest) (*officev1.GetChallengeResponse, error) {
	if len(req.GetPublicKey()) != ed25519.PublicKeySize {
		return nil, invalid("public_key must be a raw Ed25519 public key of 32 bytes")
	}
	id := auth.KeyID(req.GetPublicKey())
	s.mu.Lock()
	defer s.mu.Unlock()
	if k := s.keys[id]; k == nil || k.Revoked {
		return nil, unauthenticated("public key is not registered: ask an admin to register " + id)
	}
	now := time.Now()
	for c, ch := range s.challenges {
		if !now.Before(ch.expires) {
			delete(s.challenges, c)
		}
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	c := append([]byte(auth.ChallengePrefix), b...)
	exp := now.Add(s.opts.ChallengeTTL)
	s.challenges[string(c)] = challenge{keyID: id, expires: exp}
	return &officev1.GetChallengeResponse{Challenge: c, ExpiresAt: timestamppb.New(exp)}, nil
}

func (s *Service) ExchangeChallenge(_ context.Context, req *officev1.ExchangeChallengeRequest) (*officev1.ExchangeChallengeResponse, error) {
	if len(req.GetPublicKey()) != ed25519.PublicKeySize {
		return nil, invalid("public_key must be a raw Ed25519 public key of 32 bytes")
	}
	if len(req.GetChallenge()) == 0 || len(req.GetSignature()) == 0 {
		return nil, invalid("challenge and signature are required")
	}
	id := auth.KeyID(req.GetPublicKey())
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.challenges[string(req.GetChallenge())]
	if ok && ch.keyID == id {
		// A challenge is valid for one exchange, also if the signature is wrong.
		delete(s.challenges, string(req.GetChallenge()))
	}
	if !ok || ch.keyID != id || !time.Now().Before(ch.expires) {
		return nil, unauthenticated("challenge unknown, used or expired: get a new one")
	}
	k := s.keys[id]
	if k == nil || k.Revoked {
		return nil, unauthenticated("public key is not registered")
	}
	if !ed25519.Verify(req.GetPublicKey(), req.GetChallenge(), req.GetSignature()) {
		return nil, unauthenticated("signature does not match the challenge and the public key")
	}
	tok, exp := s.newToken("oat_", id, "", s.opts.TokenTTL)
	return &officev1.ExchangeChallengeResponse{AccessToken: tok, ExpiresAt: timestamppb.New(exp),
		ParticipantId: k.ParticipantID}, nil
}

// repeat handles a repeated command_id. It returns the stored response, or AlreadyExists if the
// command_id was used for another request. The caller holds s.mu.
func (s *Service) repeat(commandID, caller string, req proto.Message) (proto.Message, error) {
	c, ok := s.commands[commandID]
	if !ok {
		return nil, nil
	}
	if c.caller != caller || !proto.Equal(c.req, req) {
		return nil, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another request")
	}
	return c.res, nil
}

func (s *Service) CreateStageLink(ctx context.Context, req *officev1.CreateStageLinkRequest) (*officev1.CreateStageLinkResponse, error) {
	c, _ := CallerFrom(ctx)
	if c.ParticipantID == "" {
		return nil, denied("only a participant can create a stage link")
	}
	if req.GetCommandId() == "" || req.GetSessionId() == "" {
		return nil, invalid("command_id and session_id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, err := s.repeat(req.GetCommandId(), c.ParticipantID, req); err != nil {
		return nil, err
	} else if prev != nil {
		return prev.(*officev1.CreateStageLinkResponse), nil
	}
	tok, exp := s.newToken("ost_", "", req.GetSessionId(), s.opts.StageLinkTTL)
	res := &officev1.CreateStageLinkResponse{
		Url:       s.opts.PublicURL + "/stage/" + url.PathEscape(req.GetSessionId()) + "?token=" + tok,
		ExpiresAt: timestamppb.New(exp)}
	s.commands[req.GetCommandId()] = command{caller: c.ParticipantID, req: req, res: res}
	return res, nil
}

var participantIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func admin(ctx context.Context) (Caller, error) {
	c, _ := CallerFrom(ctx)
	if !c.Admin {
		return c, denied("only an admin key may register or revoke keys")
	}
	return c, nil
}

func (s *Service) RegisterKey(ctx context.Context, req *officev1.RegisterKeyRequest) (*officev1.RegisterKeyResponse, error) {
	c, err := admin(ctx)
	if err != nil {
		return nil, err
	}
	pid := req.GetParticipantId()
	switch {
	case req.GetCommandId() == "":
		return nil, invalid("command_id is required")
	case !participantIDPattern.MatchString(pid):
		return nil, invalid("participant_id must be 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit")
	case len(req.GetPublicKey()) != ed25519.PublicKeySize:
		return nil, invalid("public_key must be a raw Ed25519 public key of 32 bytes")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, err := s.repeat(req.GetCommandId(), c.ParticipantID, req); err != nil {
		return nil, err
	} else if prev != nil {
		return prev.(*officev1.RegisterKeyResponse), nil
	}
	id := auth.KeyID(req.GetPublicKey())
	res := &officev1.RegisterKeyResponse{KeyId: id}
	p := s.participants[pid]
	if p != nil {
		if req.GetKind() != officev1.ParticipantKind_PARTICIPANT_KIND_UNSPECIFIED && req.GetKind() != p.Kind {
			return nil, precondition("participant " + pid + " is registered with another kind")
		}
		if req.GetDisplayName() != "" && req.GetDisplayName() != p.DisplayName {
			return nil, precondition("participant " + pid + " is registered with another display name")
		}
	} else if req.GetDisplayName() == "" || req.GetKind() == officev1.ParticipantKind_PARTICIPANT_KIND_UNSPECIFIED {
		return nil, invalid("display_name and kind are required for a new participant")
	}
	if k := s.keys[id]; k != nil {
		switch {
		case k.Revoked:
			return nil, precondition("the key was revoked and cannot be registered again; create a new key")
		case k.ParticipantID != pid:
			return nil, connect.NewError(connect.CodeAlreadyExists, "the key belongs to another participant")
		}
		s.commands[req.GetCommandId()] = command{caller: c.ParticipantID, req: req, res: res}
		return res, nil
	}
	newParticipant := p == nil
	if newParticipant {
		s.participants[pid] = &participant{DisplayName: req.GetDisplayName(), Kind: req.GetKind()}
	}
	s.keys[id] = &key{ParticipantID: pid, PublicKey: req.GetPublicKey()}
	if err := s.save(); err != nil {
		delete(s.keys, id)
		if newParticipant {
			delete(s.participants, pid)
		}
		return nil, connect.NewError(connect.CodeInternal, "store keys: "+err.Error())
	}
	s.commands[req.GetCommandId()] = command{caller: c.ParticipantID, req: req, res: res}
	return res, nil
}

func (s *Service) RevokeKey(ctx context.Context, req *officev1.RevokeKeyRequest) (*officev1.RevokeKeyResponse, error) {
	c, err := admin(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetCommandId() == "" || req.GetKeyId() == "" {
		return nil, invalid("command_id and key_id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, err := s.repeat(req.GetCommandId(), c.ParticipantID, req); err != nil {
		return nil, err
	} else if prev != nil {
		return prev.(*officev1.RevokeKeyResponse), nil
	}
	k := s.keys[req.GetKeyId()]
	switch {
	case k == nil:
		return nil, connect.NewError(connect.CodeNotFound, "key_id is not a registered key")
	case k.admin:
		return nil, precondition("the key is in the admin file: remove it there and restart the server")
	}
	if !k.Revoked {
		k.Revoked = true
		if err := s.save(); err != nil {
			k.Revoked = false
			return nil, connect.NewError(connect.CodeInternal, "store keys: "+err.Error())
		}
	}
	// Tokens of the key stop working at once.
	for h, t := range s.tokens {
		if t.keyID == req.GetKeyId() {
			t.end()
			delete(s.tokens, h)
		}
	}
	for ch, v := range s.challenges {
		if v.keyID == req.GetKeyId() {
			delete(s.challenges, ch)
		}
	}
	res := &officev1.RevokeKeyResponse{}
	s.commands[req.GetCommandId()] = command{caller: c.ParticipantID, req: req, res: res}
	return res, nil
}
