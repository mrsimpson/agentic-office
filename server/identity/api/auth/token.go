package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"

	identityv1 "github.com/mrsimpson/agentic-office/server/identity/api"
	identityv1connect "github.com/mrsimpson/agentic-office/server/identity/api/identityv1connect"
)

// TokenSource gets access tokens with challenge and response (rs-authenticate) and keeps them
// fresh: it gets a new token when 80 % of the lifetime of the current one has passed.
// It is safe for concurrent use.
type TokenSource struct {
	identity identityv1connect.IdentityServiceClient
	key      ed25519.PrivateKey

	mu            sync.Mutex
	token         string
	participantID string
	renewAt       time.Time
}

// NewTokenSource returns a TokenSource for key. identity is a client of the auth API; it needs no
// token itself.
func NewTokenSource(identity identityv1connect.IdentityServiceClient, key ed25519.PrivateKey) *TokenSource {
	return &TokenSource{identity: identity, key: key}
}

// Token returns a valid access token. It authenticates if there is no token or it is old.
func (ts *TokenSource) Token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
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
	ts.token, ts.participantID = res.GetAccessToken(), res.GetParticipantId()
	ts.renewAt = now.Add(res.GetExpiresAt().AsTime().Sub(now) * 4 / 5)
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

// TLSConfig returns a TLS client configuration. If certFile is set, the certificates in it are
// trusted in addition to the system roots; use it for the self-signed certificate of the server.
// With an empty certFile, only the system roots are trusted.
func TLSConfig(certFile string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if certFile == "" {
		return cfg, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
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

// HTTPClient returns an HTTP client for the office server with TLSConfig(certFile). It speaks
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
