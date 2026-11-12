package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	identityv1connect "github.com/mrsimpson/agentic-office/hub/identity/api/identityv1connect"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	"github.com/mrsimpson/agentic-office/hub/identity/userapi/userv1connect"
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
	// Archive keeps the users and memberships in the hub repository (if-archive-storage).
	Archive storage.Archive
	// Bootstrap names the first system admin. It is needed at the first start only.
	Bootstrap *Bootstrap
	// Logf logs notices of the start. Optional.
	Logf func(format string, args ...any)
	// TenantFreeServices are the names of the services, besides the user API, whose calls do
	// not belong to a tenant and need no tenant header. The wiring of the server sets them.
	TenantFreeServices []string
	// StageProcedures are the procedures that a stage link token may call. The wiring of the
	// server sets them, so that identity does not need to know the other interfaces.
	StageProcedures []string
}

// Caller is who makes a call, as the token check learned it from the token (if-token-check).
type Caller = tokencheck.Caller

// CallerFrom returns the caller that the Interceptor found for this call.
func CallerFrom(ctx context.Context) (Caller, bool) { return tokencheck.CallerFrom(ctx) }

type challenge struct {
	keyID   string
	expires time.Time
}

type token struct {
	keyID        string
	stageSession string
	stageTenant  string
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

	mu         sync.Mutex
	st         *state
	files      map[string][]byte // the files of the hub repository as written
	keys       map[string]keyRef // by key ID
	challenges map[string]challenge
	tokens     map[[32]byte]*token // by SHA-256 of the token
	commands   map[string]command

	stageProcedures map[string]bool
}

var _ identityv1connect.IdentityServiceHandler = (*Service)(nil)

// New returns a Service. It reads the hub repository and checks its rules (see the user API). At
// the first start it creates the first system admin from opts.Bootstrap.
func New(ctx context.Context, opts Options) (*Service, error) {
	if opts.Archive == nil {
		return nil, errors.New("identity: no archive")
	}
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
	s := &Service{opts: opts, st: newState(), files: map[string][]byte{}, keys: map[string]keyRef{},
		challenges: map[string]challenge{}, tokens: map[[32]byte]*token{}, commands: map[string]command{},
		stageProcedures: map[string]bool{}}
	for _, p := range opts.StageProcedures {
		s.stageProcedures[p] = true
	}
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func invalid(msg string) error         { return connect.NewError(connect.CodeInvalidArgument, msg) }
func unauthenticated(msg string) error { return connect.NewError(connect.CodeUnauthenticated, msg) }
func denied(msg string) error          { return connect.NewError(connect.CodePermissionDenied, msg) }
func precondition(msg string) error    { return connect.NewError(connect.CodeFailedPrecondition, msg) }

// tenantFree reports whether a call does not belong to a tenant: the public procedures, the user
// API and the services that the wiring names in Options.TenantFreeServices.
func (s *Service) tenantFree(procedure string) bool {
	if publicProcedures[procedure] {
		return true
	}
	for _, name := range append([]string{userv1connect.UserServiceName}, s.opts.TenantFreeServices...) {
		if strings.HasPrefix(procedure, "/"+name+"/") {
			return true
		}
	}
	return false
}

// publicProcedures need no token.
var publicProcedures = map[string]bool{
	identityv1connect.IdentityServiceGetChallengeProcedure:      true,
	identityv1connect.IdentityServiceExchangeChallengeProcedure: true,
}

// Interceptor is the token check for every call to the server. It reads the token from the
// header "Authorization: Bearer <token>". Calls without a valid token fail with Unauthenticated,
// except GetChallenge and ExchangeChallenge. A stage link token may only call the procedures in
// Options.StageProcedures. The caller is available to the handlers through CallerFrom.
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
			if c.StageSessionID != "" {
				if !s.stageProcedures[spec.Procedure] {
					return denied("a stage link only allows reading the session stream")
				}
				return next(tokencheck.WithCaller(ctx, c), spec, stream)
			}
			if !s.tenantFree(spec.Procedure) {
				// The calls that belong to a tenant name it (auth.TenantHeader). A caller who is
				// no member, a system admin too, gets the answer for a tenant that does not exist.
				var name string
				if info, ok := connect.CallInfoForServerContext(ctx); ok {
					name = info.RequestHeader().Get(auth.TenantHeader)
				}
				if name == "" {
					return invalid("name the tenant: send the header " + auth.TenantHeader)
				}
				role, member := c.Member(name)
				if !member {
					return notFound("no such tenant")
				}
				c.TenantID, c.TenantRole = name, role
			}
			return next(tokencheck.WithCaller(ctx, c), spec, stream)
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
		// A stage link ends with its tenant.
		if tn := s.st.tenants[t.stageTenant]; tn == nil || !tn.active(time.Now()) {
			return Caller{}, unauthenticated("the tenant of the stage link was removed")
		}
		return Caller{StageSessionID: t.stageSession, TenantID: t.stageTenant, Expires: t.expires, Done: t.done}, nil
	}
	now := time.Now()
	k, u, ok := s.usable(t.keyID, now)
	if !ok {
		return Caller{}, unauthenticated("key revoked or user removed")
	}
	c := Caller{ParticipantID: k.userID, DisplayName: u.DisplayName, Kind: callerKind(kindOf(u.Kind)), KeyID: t.keyID,
		SystemAdmin: u.SystemAdmin, Expires: t.expires, Done: t.done}
	for mk, m := range s.st.members {
		if mk.user == k.userID && m.active(now) {
			role := tokencheck.RoleMember
			if m.Role == roleTenantAdmin {
				role = tokencheck.RoleTenantAdmin
			}
			c.Tenants = append(c.Tenants, tokencheck.Membership{TenantID: mk.tenant, Role: role})
		}
	}
	sort.Slice(c.Tenants, func(i, j int) bool { return c.Tenants[i].TenantID < c.Tenants[j].TenantID })
	return c, nil
}

// newToken creates a token. The caller holds s.mu.
func (s *Service) newToken(prefix, keyID, stageSession, stageTenant string, ttl time.Duration) (string, time.Time) {
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
	t := &token{keyID: keyID, stageSession: stageSession, stageTenant: stageTenant, expires: now.Add(ttl), done: make(chan struct{})}
	t.timer = time.AfterFunc(ttl, t.end)
	s.tokens[sha256.Sum256([]byte(tok))] = t
	return tok, t.expires
}

func (s *Service) GetChallenge(_ context.Context, req *identityv1.GetChallengeRequest) (*identityv1.GetChallengeResponse, error) {
	if len(req.GetPublicKey()) != ed25519.PublicKeySize {
		return nil, invalid("public_key must be a raw Ed25519 public key of 32 bytes")
	}
	id := auth.KeyID(req.GetPublicKey())
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, _, ok := s.usable(id, time.Now()); !ok {
		return nil, unauthenticated("public key is not registered: ask a system admin to register " + id)
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
	return &identityv1.GetChallengeResponse{Challenge: c, ExpiresAt: timestamppb.New(exp)}, nil
}

func (s *Service) ExchangeChallenge(_ context.Context, req *identityv1.ExchangeChallengeRequest) (*identityv1.ExchangeChallengeResponse, error) {
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
	k, _, ok := s.usable(id, time.Now())
	if !ok {
		return nil, unauthenticated("public key is not registered")
	}
	if !ed25519.Verify(req.GetPublicKey(), req.GetChallenge(), req.GetSignature()) {
		return nil, unauthenticated("signature does not match the challenge and the public key")
	}
	tok, exp := s.newToken("oat_", id, "", "", s.opts.TokenTTL)
	return &identityv1.ExchangeChallengeResponse{AccessToken: tok, ExpiresAt: timestamppb.New(exp),
		ParticipantId: k.userID}, nil
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

func (s *Service) CreateStageLink(ctx context.Context, req *identityv1.CreateStageLinkRequest) (*identityv1.CreateStageLinkResponse, error) {
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
		return prev.(*identityv1.CreateStageLinkResponse), nil
	}
	tok, exp := s.newToken("ost_", "", req.GetSessionId(), c.TenantID, s.opts.StageLinkTTL)
	res := &identityv1.CreateStageLinkResponse{
		Url:       s.opts.PublicURL + "/stage/" + url.PathEscape(req.GetSessionId()) + "?token=" + tok,
		ExpiresAt: timestamppb.New(exp)}
	s.commands[req.GetCommandId()] = command{caller: c.ParticipantID, req: req, res: res}
	return res, nil
}

var participantIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// callerKind maps the actor kind of the auth API to the kind of the token check.
func callerKind(k identityv1.ActorKind) tokencheck.Kind {
	switch k {
	case identityv1.ActorKind_ACTOR_KIND_HUMAN:
		return tokencheck.KindHuman
	case identityv1.ActorKind_ACTOR_KIND_AGENT:
		return tokencheck.KindAgent
	}
	return tokencheck.KindUnknown
}
