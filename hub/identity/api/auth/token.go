package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	identityv1connect "github.com/mrsimpson/agentic-office/hub/identity/api/identityv1connect"
)

// TokenSource gets access tokens with challenge and response (rs-authenticate) and keeps them
// fresh: it gets a new token when 80 % of the lifetime of the current one has passed.
// With AsAgent it then asks for a token as a team agent (dec-team-agents) and uses that one. A
// source made by NewFileTokenSource has no key: it reads the token that another process keeps in
// a file. It is safe for concurrent use.
type TokenSource struct {
	identity identityv1connect.IdentityServiceClient
	key      ed25519.PrivateKey
	agent    *AgentSpec
	file     string

	mu            sync.Mutex
	token         string
	participantID string
	displayName   string
	expires       time.Time
	renewAt       time.Time
}

// NewTokenSource returns a TokenSource for key. identity is a client of the auth API; it needs no
// token itself.
func NewTokenSource(identity identityv1connect.IdentityServiceClient, key ed25519.PrivateKey) *TokenSource {
	return &TokenSource{identity: identity, key: key}
}

// AgentSpec names the team agent that a token source works as: its tenant and ID, and the version
// of the definition that the agent launcher confirmed.
type AgentSpec struct {
	Tenant, Agent, Version string
}

// AsAgent makes ts ask for a token as the team agent after it has authenticated with its key. It
// returns ts. Call it before the first use.
func (ts *TokenSource) AsAgent(a AgentSpec) *TokenSource {
	ts.agent = &a
	return ts
}

// NewFileTokenSource returns a TokenSource that has no key. It reads the token from the file that
// WriteTokenFile writes; another process renews it. A harness gets its token this way, never the
// key of its agent launcher.
func NewFileTokenSource(path string) *TokenSource { return &TokenSource{file: path} }

// tokenFile is the content of a token file.
type tokenFile struct {
	Token         string    `json:"token"`
	ParticipantID string    `json:"participant_id"`
	DisplayName   string    `json:"display_name"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// WriteTokenFile writes the token and who it belongs to into a file that only the user reads, so
// that NewFileTokenSource reads it. The file is replaced in one step.
func WriteTokenFile(path, token, participantID, displayName string, expiresAt time.Time) error {
	b, err := json.Marshal(tokenFile{token, participantID, displayName, expiresAt})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Current returns the token, the participant, the display name and the end of the current token
// without a new call, or empty values if there is none yet.
func (ts *TokenSource) Current() (token, participantID, displayName string, expiresAt time.Time) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.token, ts.participantID, ts.displayName, ts.expires
}

// Token returns a valid access token. It authenticates if there is no token or it is old.
func (ts *TokenSource) Token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.file != "" {
		return ts.readFile()
	}
	if ts.token != "" && time.Now().Before(ts.renewAt) {
		return ts.token, nil
	}
	pub := PublicKey(ts.key)
	ch, err := ts.identity.GetChallenge(ctx, &identityv1.GetChallengeRequest{PublicKey: pub})
	if err != nil {
		return "", fmt.Errorf("get challenge: %w", err)
	}
	sig, err := SignChallenge(ts.key, ch.GetChallenge())
	if err != nil {
		return "", err
	}
	now := time.Now()
	res, err := ts.identity.ExchangeChallenge(ctx, &identityv1.ExchangeChallengeRequest{
		PublicKey: pub, Challenge: ch.GetChallenge(), Signature: sig})
	if err != nil {
		return "", fmt.Errorf("exchange challenge: %w", err)
	}
	tok, pid, name, exp := res.GetAccessToken(), res.GetParticipantId(), res.GetDisplayName(), res.GetExpiresAt().AsTime()
	if a := ts.agent; a != nil {
		// The call names the tenant in its request and carries the token of the member.
		actx, info := connect.NewClientContext(ctx)
		info.RequestHeader().Set(AuthorizationHeader, "Bearer "+tok)
		ares, err := ts.identity.IssueAgentToken(actx, &identityv1.IssueAgentTokenRequest{TenantId: a.Tenant, AgentId: a.Agent,
			DefinitionVersion: a.Version})
		if err != nil {
			return "", fmt.Errorf("token as the team agent %s: %w", a.Agent, err)
		}
		tok, pid, name, exp = ares.GetAccessToken(), ares.GetParticipantId(), ares.GetDisplayName(), ares.GetExpiresAt().AsTime()
	}
	ts.token, ts.participantID, ts.displayName, ts.expires = tok, pid, name, exp
	ts.renewAt = now.Add(exp.Sub(now) * 4 / 5)
	return ts.token, nil
}

// readFile reads the token file. The caller holds ts.mu.
func (ts *TokenSource) readFile() (string, error) {
	b, err := os.ReadFile(ts.file)
	if err != nil {
		return "", fmt.Errorf("token file: %w", err)
	}
	var f tokenFile
	if err := json.Unmarshal(b, &f); err != nil || f.Token == "" {
		return "", fmt.Errorf("token file %s is not a token file", ts.file)
	}
	ts.token, ts.participantID, ts.displayName, ts.expires = f.Token, f.ParticipantID, f.DisplayName, f.ExpiresAt
	return ts.token, nil
}

// ParticipantID returns the participant the key belongs to. It authenticates if needed.
func (ts *TokenSource) ParticipantID(ctx context.Context) (string, error) {
	if _, err := ts.Token(ctx); err != nil {
		return "", err
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.participantID, nil
}

// DisplayName returns the display name of the participant the key belongs to. It authenticates if
// needed.
func (ts *TokenSource) DisplayName(ctx context.Context) (string, error) {
	if _, err := ts.Token(ctx); err != nil {
		return "", err
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.displayName, nil
}

// Invalidate drops the current token, so the next call authenticates again. Bearer calls it when
// the server answers "unauthenticated", for example after a restart of the server.
func (ts *TokenSource) Invalidate() {
	ts.mu.Lock()
	ts.token = ""
	ts.mu.Unlock()
}

// AuthorizationHeader is the request header that carries the token: "Bearer <token>".
const AuthorizationHeader = "Authorization"

// Bearer returns a client interceptor that sends a token of ts with every call. If a call fails
// with "unauthenticated", the token is dropped; the caller may simply repeat the call (commands
// are idempotent, dec-idempotent-commands).
func Bearer(ts *TokenSource) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			tok, err := ts.Token(ctx)
			if err != nil {
				return nil, err
			}
			setToken(ctx, tok)
			s, err := next(ctx, spec)
			if err != nil {
				return nil, err
			}
			return &invalidating{ClientStream: s, ts: ts}, nil
		}
	}
}

// StaticToken returns a client interceptor that sends a fixed token, for example the token of a
// stage link.
func StaticToken(token string) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			setToken(ctx, token)
			return next(ctx, spec)
		}
	}
}

// TenantHeader is the header that names the tenant of a call. Every call of the session, the
// records, the tasks, the slides and the stage link carries it; the calls of the auth API, the
// user API and the tenant API do not. A caller who is no member of the tenant gets "not found",
// the same answer as for a tenant that does not exist.
const TenantHeader = "Office-Tenant"

// Tenant returns a client interceptor that names the tenant of every call. With an empty ID it
// sends nothing.
func Tenant(id string) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			if info, ok := connect.CallInfoForClientContext(ctx); ok && id != "" {
				info.RequestHeader().Set(TenantHeader, id)
			}
			return next(ctx, spec)
		}
	}
}

func setToken(ctx context.Context, token string) {
	if info, ok := connect.CallInfoForClientContext(ctx); ok {
		info.RequestHeader().Set(AuthorizationHeader, "Bearer "+token)
	}
}

// TokenFromHeader returns the token of an Authorization header value, or "".
func TokenFromHeader(v string) string {
	if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return ""
}

type invalidating struct {
	connect.ClientStream
	ts *TokenSource
}

func (s *invalidating) Receive(msg any) error {
	err := s.ClientStream.Receive(msg)
	if connect.CodeOf(err) == connect.CodeUnauthenticated {
		s.ts.Invalidate()
	}
	return err
}

// TLSConfig returns a TLS client configuration. If certFile is set, only the certificates in it
// are trusted; use it for the self-signed certificate of the server. Go checks them itself on
// every system: with the system roots in the pool, macOS would check them by its own rules for
// public server certificates and reject a self-signed certificate. With an empty certFile, only
// the system roots are trusted.
func TLSConfig(certFile string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if certFile == "" {
		return cfg, nil
	}
	pool := x509.NewCertPool()
	b, err := os.ReadFile(certFile)
	if err != nil {
		return nil, err
	}
	if !pool.AppendCertsFromPEM(b) {
		return nil, errors.New(certFile + ": no PEM certificate")
	}
	cfg.RootCAs = pool
	return cfg, nil
}

// HTTPClient returns an HTTP client for the office hub with TLSConfig(certFile). It speaks
// HTTP/2 when the server offers it.
func HTTPClient(certFile string) (*http.Client, error) {
	cfg, err := TLSConfig(certFile)
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = cfg
	tr.ForceAttemptHTTP2 = true
	return &http.Client{Transport: tr}, nil
}
