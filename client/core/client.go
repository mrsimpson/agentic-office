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

	commands sessioncommandsv1connect.SessionCommandsServiceClient
	stream   sessionstreamv1connect.SessionStreamServiceClient
	// sessionImport imports conversations (if-session-import); see Import.
	sessionImport sessionimportv1connect.SessionImportServiceClient
	identity      identityv1connect.IdentityServiceClient
	users         userv1connect.UserServiceClient
	tenants       tenantv1connect.TenantServiceClient
	decision      decisionv1connect.DecisionServiceClient
	minutes       minutesv1connect.MinutesServiceClient
	note          notev1connect.NoteServiceClient
	task          taskv1connect.TaskServiceClient
	slides        slidesv1connect.SlidesServiceClient
	document      documentv1connect.DocumentServiceClient
	archive       archivev1connect.ArchiveReadServiceClient
}

var _ coreapi.Dial = Dial

// Dial reads the key of cfg and returns a client (Dial). It does not connect yet; the
// first call authenticates (rs-authenticate).
func Dial(cfg coreapi.Config) (coreapi.Client, error) {
	key, err := auth.ReadKey(cfg.KeyFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w %s: create one with \"office key create\" and let a system admin register it", coreapi.ErrNoKey, cfg.KeyFile)
	}
	if err != nil {
		return nil, err
	}
	hc, err := auth.HTTPClient(cfg.CACert)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(cfg.Hub, "/")
	// The identity client inside the token source must not use Bearer itself.
	ts := auth.NewTokenSource(identityv1connect.NewIdentityServiceClient(newConnect(hc, base)), key)
	cc := newConnect(hc, base, auth.Bearer(ts), auth.Tenant(cfg.Tenant))
	return &hubClient{cfg: cfg, key: key, tokens: ts,
		commands:      sessioncommandsv1connect.NewSessionCommandsServiceClient(cc),
		stream:        sessionstreamv1connect.NewSessionStreamServiceClient(cc),
		sessionImport: sessionimportv1connect.NewSessionImportServiceClient(cc),
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

func (c *hubClient) KeyID() string { return auth.KeyID(auth.PublicKey(c.key)) }

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
