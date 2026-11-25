package core

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/mrsimpson/agentic-office/hub/archive/api/archivev1connect"
	"github.com/mrsimpson/agentic-office/hub/documents/api/documentv1connect"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/identity/api/identityv1connect"
	"github.com/mrsimpson/agentic-office/hub/identity/userapi/userv1connect"
	"github.com/mrsimpson/agentic-office/hub/records/decision/api/decisionv1connect"
	"github.com/mrsimpson/agentic-office/hub/records/minutes/api/minutesv1connect"
	"github.com/mrsimpson/agentic-office/hub/records/note/api/notev1connect"
	"github.com/mrsimpson/agentic-office/hub/session/commandsapi/sessioncommandsv1connect"
	"github.com/mrsimpson/agentic-office/hub/session/importapi/sessionimportv1connect"
	"github.com/mrsimpson/agentic-office/hub/session/listapi/sessionlistv1connect"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/sessionstreamv1connect"
	"github.com/mrsimpson/agentic-office/hub/slides/api/slidesv1connect"
	"github.com/mrsimpson/agentic-office/hub/tasks/api/taskv1connect"
	"github.com/mrsimpson/agentic-office/hub/tenant/api/tenantv1connect"
)

// hubClient is the client of the office hub for one actor (one key). It implements
// Client.
type hubClient struct {
	cfg    coreapi.Config
	key    ed25519.PrivateKey
	tokens *auth.TokenSource

	connection *connect.Client
	commands   sessioncommandsv1connect.SessionCommandsServiceClient
	stream     sessionstreamv1connect.SessionStreamServiceClient
	// sessionImport imports conversations (if-session-import); see Import.
	sessionImport sessionimportv1connect.SessionImportServiceClient
	// sessionList lists the sessions that the participant may see (if-session-list).
	sessionList sessionlistv1connect.SessionListServiceClient
	identity    identityv1connect.IdentityServiceClient
	users       userv1connect.UserServiceClient
	tenants     tenantv1connect.TenantServiceClient
	decision    decisionv1connect.DecisionServiceClient
	minutes     minutesv1connect.MinutesServiceClient
	note        notev1connect.NoteServiceClient
	task        taskv1connect.TaskServiceClient
	slides      slidesv1connect.SlidesServiceClient
	document    documentv1connect.DocumentServiceClient
	archive     archivev1connect.ArchiveReadServiceClient
}

var _ coreapi.Dial = Dial

// Dial reads the key of cfg and returns a client (Dial). It does not connect yet; the
// first call authenticates (rs-authenticate).
func Dial(cfg coreapi.Config) (coreapi.Client, error) {
	hc, err := auth.HTTPClient(cfg.CACert)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(cfg.Hub, "/")
	var key ed25519.PrivateKey
	var ts *auth.TokenSource
	if cfg.TokenFile != "" {
		// A harness has the token of its team agent and no key (dec-team-agents).
		ts = auth.NewFileTokenSource(cfg.TokenFile)
	} else {
		if key, err = auth.ReadKey(cfg.KeyFile); errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w %s: create one with \"office key create\" and let a system admin register it", coreapi.ErrNoKey, cfg.KeyFile)
		} else if err != nil {
			return nil, err
		}
		// The identity client inside the token source must not use Bearer itself.
		ts = auth.NewTokenSource(identityv1connect.NewIdentityServiceClient(newConnect(hc, base)), key)
		if cfg.TeamAgent != "" {
			ts.AsAgent(auth.AgentSpec{Tenant: cfg.Tenant, Agent: cfg.TeamAgent, Version: cfg.DefinitionVersion})
		}
	}
	cc := newConnect(hc, base, auth.Bearer(ts), auth.Tenant(cfg.Tenant))
	return &hubClient{cfg: cfg, key: key, tokens: ts, connection: cc,
		commands:      sessioncommandsv1connect.NewSessionCommandsServiceClient(cc),
		stream:        sessionstreamv1connect.NewSessionStreamServiceClient(cc),
		sessionImport: sessionimportv1connect.NewSessionImportServiceClient(cc),
		sessionList:   sessionlistv1connect.NewSessionListServiceClient(cc),
		identity:      identityv1connect.NewIdentityServiceClient(cc),
		users:         userv1connect.NewUserServiceClient(cc),
		tenants:       tenantv1connect.NewTenantServiceClient(cc),
		decision:      decisionv1connect.NewDecisionServiceClient(cc),
		minutes:       minutesv1connect.NewMinutesServiceClient(cc),
		note:          notev1connect.NewNoteServiceClient(cc),
		task:          taskv1connect.NewTaskServiceClient(cc),
		slides:        slidesv1connect.NewSlidesServiceClient(cc),
		document:      documentv1connect.NewDocumentServiceClient(cc),
		archive:       archivev1connect.NewArchiveReadServiceClient(cc),
	}, nil
}

func (c *hubClient) Config() coreapi.Config { return c.cfg }

func (c *hubClient) Connection() *connect.Client { return c.connection }

func (c *hubClient) KeyID() string {
	if c.key == nil {
		return ""
	}
	return auth.KeyID(auth.PublicKey(c.key))
}

func (c *hubClient) Commands() sessioncommandsv1connect.SessionCommandsServiceClient {
	return c.commands
}

func (c *hubClient) Stream() sessionstreamv1connect.SessionStreamServiceClient { return c.stream }

func newConnect(hc *http.Client, base string, ic ...connect.ClientInterceptor) *connect.Client {
	return connect.NewClient(connecthttp.NewTransport(hc, base), ic...)
}

// ParticipantID authenticates if needed and returns the participant of the key.
func (c *hubClient) ParticipantID(ctx context.Context) (string, error) {
	var pid string
	err := retry(ctx, func() (err error) {
		pid, err = c.tokens.ParticipantID(ctx)
		return err
	})
	return pid, err
}

// DisplayName authenticates if needed and returns the display name of the participant.
func (c *hubClient) DisplayName(ctx context.Context) (string, error) {
	var name string
	err := retry(ctx, func() (err error) {
		name, err = c.tokens.DisplayName(ctx)
		return err
	})
	return name, err
}

// WriteTokenFile writes the access token, with the participant and its display name, into a file.
func (c *hubClient) WriteTokenFile(ctx context.Context, path string) error {
	if _, err := c.AccessToken(ctx); err != nil {
		return err
	}
	tok, pid, name, exp := c.tokens.Current()
	return auth.WriteTokenFile(path, tok, pid, name, exp)
}

// AccessToken authenticates if needed and returns a valid access token.
func (c *hubClient) AccessToken(ctx context.Context) (string, error) {
	var tok string
	err := retry(ctx, func() (err error) {
		tok, err = c.tokens.Token(ctx)
		return err
	})
	return tok, err
}

// retry runs f and runs it once more if it failed with "unauthenticated" (the token was
// dropped, for example after a restart of the server). Commands are idempotent, so this is
// safe as long as f reuses its command ID.
func retry(ctx context.Context, f func() error) error {
	err := f()
	if connect.CodeOf(err) == connect.CodeUnauthenticated && ctx.Err() == nil {
		err = f()
	}
	return err
}
