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
	// PortalLinkTTL is how long the code of a portal link can be exchanged. Default 5 minutes.
	PortalLinkTTL time.Duration
	// PortalSessionTTL is the lifetime of the access token that a portal link gives. Default 1
	// hour.
	PortalSessionTTL time.Duration
	// PublicURL is the base URL of the server in stage links, for example
	// "https://office.example.org:8740".
	PublicURL string
	// Archive keeps the users and memberships in the hub repository (if-archive-storage).
	Archive storage.Archive
	// Bootstrap names the first system admin. It is needed at the first start only.
	Bootstrap *Bootstrap
	// BootstrapErr is the error of a first system admin that could not be parsed. Only the first
	// start fails with it: later starts ignore the flag, whatever it holds.
	BootstrapErr error
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

// portalLink is the code of a portal link that has not been exchanged yet.
type portalLink struct {
	keyID   string
	tenant  string // the tenant whose pages the portal opens first, or empty
	expires time.Time
}

type challenge struct {
	keyID   string
	expires time.Time
}

type token struct {
	keyID string
	// A token as a team agent (dec-team-agents) has the key of the agent launcher in keyID, so it
	// ends with that key, and names the agent, its tenant and the confirmed version.
	agent        string
	agentTenant  string
	version      string
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

	mu          sync.Mutex
	st          *state
	files       map[string][]byte // the files of the hub repository as written
	keys        map[string]keyRef // by key ID
	challenges  map[string]challenge
	portalLinks map[[32]byte]portalLink // by the hash of the code
	tokens      map[[32]byte]*token     // by SHA-256 of the token
	commands    map[string]command

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
	if opts.PortalLinkTTL <= 0 {
		opts.PortalLinkTTL = 5 * time.Minute
	}
	if opts.PortalSessionTTL <= 0 {
		opts.PortalSessionTTL = time.Hour
	}
	opts.PublicURL = strings.TrimSuffix(opts.PublicURL, "/")
	s := &Service{opts: opts, st: newState(), files: map[string][]byte{}, keys: map[string]keyRef{},
		challenges: map[string]challenge{}, portalLinks: map[[32]byte]portalLink{}, tokens: map[[32]byte]*token{}, commands: map[string]command{},
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
	if publicProcedures[procedure] || procedure == identityv1connect.IdentityServiceCreatePortalLinkProcedure ||
		procedure == identityv1connect.IdentityServiceWhoAmIProcedure ||
		procedure == identityv1connect.IdentityServiceIssueAgentTokenProcedure {
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
	identityv1connect.IdentityServiceGetChallengeProcedure:       true,
	identityv1connect.IdentityServiceExchangeChallengeProcedure:  true,
	identityv1connect.IdentityServiceExchangePortalLinkProcedure: true,
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
					return denied("a stage link only allows reading the session")
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
					return invalid("name the tenant of the call: send the header " + auth.TenantHeader + " (the office CLI: --tenant or OFFICE_TENANT)")
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
	if t.agent != "" {
		return s.agentCaller(t, k.userID, u, now)
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

// agentCaller is the caller of a token as a team agent. The token ends when the agent or its
// tenant ends and when the agent launcher leaves the tenant. The caller holds s.mu.
func (s *Service) agentCaller(t *token, launcher string, lu *userRecord, now time.Time) (Caller, error) {
	a, tn := s.st.users[t.agent], s.st.tenants[t.agentTenant]
	if a == nil || !a.active(now) || a.Team != t.agentTenant || tn == nil || !tn.active(now) {
		return Caller{}, unauthenticated("the team agent was removed")
	}
	m := s.st.members[memberKey{t.agentTenant, launcher}]
	if m == nil || !m.active(now) {
		return Caller{}, unauthenticated("the agent launcher is no member of the tenant any more")
	}
	return Caller{ParticipantID: t.agent, DisplayName: agentDisplayName(a.DisplayName, lu.DisplayName, t.version),
		Kind: tokencheck.KindAgent, TeamTenant: t.agentTenant, AgentLauncher: launcher, LauncherName: lu.DisplayName,
		DefinitionVersion: t.version, Expires: t.expires, Done: t.done,
		Tenants: []tokencheck.Membership{{TenantID: t.agentTenant, Role: tokencheck.RoleMember}}}, nil
}

// agentDisplayName is the name of a team agent as its token shows it, also as the author of what
// it writes: the agent, the agent launcher and the version of the definition.
func agentDisplayName(agent, launcher, version string) string {
	if len(version) > 12 {
		version = version[:12]
	}
	if version == "" {
		version = "unknown"
	}
	return agent + " (agent launcher " + launcher + ", definition " + version + ")"
}

var _ tokencheck.Kinds = (*Service)(nil)

// KindOf implements tokencheck.Kinds.
func (s *Service) KindOf(participantID string) tokencheck.Kind {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.st.users[participantID]
	if u == nil {
		return tokencheck.KindUnknown
	}
	return callerKind(kindOf(u.Kind))
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
	k, u, ok := s.usable(id, time.Now())
	if !ok {
		return nil, unauthenticated("public key is not registered")
	}
	if !ed25519.Verify(req.GetPublicKey(), req.GetChallenge(), req.GetSignature()) {
		return nil, unauthenticated("signature does not match the challenge and the public key")
	}
	tok, exp := s.newToken("oat_", id, "", "", s.opts.TokenTTL)
	return &identityv1.ExchangeChallengeResponse{AccessToken: tok, ExpiresAt: timestamppb.New(exp),
		ParticipantId: k.userID, DisplayName: u.DisplayName}, nil
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

// CreatePortalLink implements the RPC. Every user may call it (dec-web-portal).
func (s *Service) CreatePortalLink(ctx context.Context, req *identityv1.CreatePortalLinkRequest) (*identityv1.CreatePortalLinkResponse, error) {
	c, _ := CallerFrom(ctx)
	if c.ParticipantID == "" || c.TeamTenant != "" {
		return nil, denied("a stage link or a team agent cannot create a portal link")
	}
	if req.GetCommandId() == "" {
		return nil, invalid("command_id is required")
	}
	if t := req.GetTenantId(); t != "" {
		if _, member := c.Member(t); !member {
			// The same answer as for a tenant that does not exist.
			return nil, connect.NewError(connect.CodeNotFound, "no such tenant")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, err := s.repeat(req.GetCommandId(), c.ParticipantID, req); err != nil {
		return nil, err
	} else if prev != nil {
		return prev.(*identityv1.CreatePortalLinkResponse), nil
	}
	now := time.Now()
	for h, l := range s.portalLinks {
		if !now.Before(l.expires) {
			delete(s.portalLinks, h)
		}
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	code := "opl_" + base64.RawURLEncoding.EncodeToString(b)
	exp := now.Add(s.opts.PortalLinkTTL)
	s.portalLinks[sha256.Sum256([]byte(code))] = portalLink{keyID: c.KeyID, tenant: req.GetTenantId(), expires: exp}
	res := &identityv1.CreatePortalLinkResponse{Url: s.opts.PublicURL + "/portal/login?code=" + code, ExpiresAt: timestamppb.New(exp)}
	s.commands[req.GetCommandId()] = command{caller: c.ParticipantID, req: req, res: res}
	return res, nil
}

// ExchangePortalLink implements the RPC. The code works once.
func (s *Service) ExchangePortalLink(_ context.Context, req *identityv1.ExchangePortalLinkRequest) (*identityv1.ExchangePortalLinkResponse, error) {
	if req.GetCode() == "" {
		return nil, invalid("code is required")
	}
	h := sha256.Sum256([]byte(req.GetCode()))
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.portalLinks[h]
	delete(s.portalLinks, h)
	if !ok || !time.Now().Before(l.expires) {
		return nil, unauthenticated("the portal link is unknown, used or expired: ask for a new one")
	}
	k, _, ok := s.usable(l.keyID, time.Now())
	if !ok {
		return nil, unauthenticated("the key of the portal link is not valid any more")
	}
	tok, exp := s.newToken("oat_", l.keyID, "", "", s.opts.PortalSessionTTL)
	return &identityv1.ExchangePortalLinkResponse{AccessToken: tok, ExpiresAt: timestamppb.New(exp), ParticipantId: k.userID,
		TenantId: l.tenant}, nil
}

// IssueAgentToken implements the RPC: a human member of a tenant gets a token as a team agent
// of the tenant (dec-team-agents).
func (s *Service) IssueAgentToken(ctx context.Context, req *identityv1.IssueAgentTokenRequest) (*identityv1.IssueAgentTokenResponse, error) {
	c, _ := CallerFrom(ctx)
	if c.ParticipantID == "" || c.TeamTenant != "" || c.Kind != tokencheck.KindHuman {
		return nil, denied("only a human member can start a team agent")
	}
	if req.GetTenantId() == "" || req.GetAgentId() == "" {
		return nil, invalid("tenant_id and agent_id are required")
	}
	v := req.GetDefinitionVersion()
	if len(v) > 200 || strings.ContainsFunc(v, func(r rune) bool { return r < ' ' || r == 0x7f || r == '<' || r == '>' }) {
		return nil, invalid("definition_version must be at most 200 characters without control characters")
	}
	// The same answer for a tenant that the caller is not a member of, and for an agent that does
	// not exist.
	missing := notFound("no such team agent")
	if _, member := c.Member(req.GetTenantId()); !member {
		return nil, missing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	a, tn := s.st.users[req.GetAgentId()], s.st.tenants[req.GetTenantId()]
	if a == nil || a.Team != req.GetTenantId() || !a.active(now) || tn == nil || !tn.active(now) {
		return nil, missing
	}
	tok, exp := s.newToken("oat_", c.KeyID, "", "", s.opts.TokenTTL)
	t := s.tokens[sha256.Sum256([]byte(tok))]
	t.agent, t.agentTenant, t.version = req.GetAgentId(), req.GetTenantId(), v
	return &identityv1.IssueAgentTokenResponse{AccessToken: tok, ExpiresAt: timestamppb.New(exp), ParticipantId: req.GetAgentId(),
		DisplayName: agentDisplayName(a.DisplayName, c.DisplayName, v)}, nil
}

// WhoAmI implements the RPC: the caller as the token check knows it.
func (s *Service) WhoAmI(ctx context.Context, _ *identityv1.WhoAmIRequest) (*identityv1.WhoAmIResponse, error) {
	c, _ := CallerFrom(ctx)
	if c.ParticipantID == "" {
		return nil, denied("a stage link has no user")
	}
	kind := identityv1.ActorKind_ACTOR_KIND_HUMAN
	if c.Kind == tokencheck.KindAgent {
		kind = identityv1.ActorKind_ACTOR_KIND_AGENT
	}
	res := &identityv1.WhoAmIResponse{ParticipantId: c.ParticipantID, DisplayName: c.DisplayName, Kind: kind, SystemAdmin: c.SystemAdmin}
	for _, m := range c.Tenants {
		res.Memberships = append(res.Memberships, &identityv1.TenantMembership{TenantId: m.TenantID,
			TenantAdmin: m.Role == tokencheck.RoleTenantAdmin})
	}
	return res, nil
}
