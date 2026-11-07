package core

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/identity/api/identityv1connect"
	"github.com/mrsimpson/agentic-office/hub/records/decision/api/decisionv1connect"
	"github.com/mrsimpson/agentic-office/hub/records/minutes/api/minutesv1connect"
	"github.com/mrsimpson/agentic-office/hub/records/note/api/notev1connect"
	"github.com/mrsimpson/agentic-office/hub/session/api/sessionv1connect"
	"github.com/mrsimpson/agentic-office/hub/slides/api/slidesv1connect"
	"github.com/mrsimpson/agentic-office/hub/tasks/api/taskv1connect"
)

// Client is an authenticated client of the office hub for one actor (one key).
type Client struct {
	Config
	Key    ed25519.PrivateKey
	Tokens *auth.TokenSource

	Session  sessionv1connect.SessionServiceClient
	Identity identityv1connect.IdentityServiceClient
	Decision decisionv1connect.DecisionServiceClient
	Minutes  minutesv1connect.MinutesServiceClient
	Note     notev1connect.NoteServiceClient
	Task     taskv1connect.TaskServiceClient
	Slides   slidesv1connect.SlidesServiceClient
}

// ErrNoKey means that the key file does not exist.
var ErrNoKey = errors.New("no key file")

// Dial reads the key of cfg and returns a client. It does not connect yet; the first call
// authenticates (rs-authenticate).
func Dial(cfg Config) (*Client, error) {
	key, err := auth.ReadKey(cfg.KeyFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w %s: create one with \"office key create\" and let an admin register it", ErrNoKey, cfg.KeyFile)
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
	cc := newConnect(hc, base, auth.Bearer(ts))
	return &Client{Config: cfg, Key: key, Tokens: ts,
		Session:  sessionv1connect.NewSessionServiceClient(cc),
		Identity: identityv1connect.NewIdentityServiceClient(cc),
		Decision: decisionv1connect.NewDecisionServiceClient(cc),
		Minutes:  minutesv1connect.NewMinutesServiceClient(cc),
		Note:     notev1connect.NewNoteServiceClient(cc),
		Task:     taskv1connect.NewTaskServiceClient(cc),
		Slides:   slidesv1connect.NewSlidesServiceClient(cc),
	}, nil
}

func newConnect(hc *http.Client, base string, ic ...connect.ClientInterceptor) *connect.Client {
	return connect.NewClient(connecthttp.NewTransport(hc, base), ic...)
}

// ParticipantID authenticates if needed and returns the participant of the key.
func (c *Client) ParticipantID(ctx context.Context) (string, error) {
	var pid string
	err := Retry(ctx, func() (err error) {
		pid, err = c.Tokens.ParticipantID(ctx)
		return err
	})
	return pid, err
}

// Retry runs f and runs it once more if it failed with "unauthenticated" (the token was
// dropped, for example after a restart of the server). Commands are idempotent, so this is
// safe as long as f reuses its command ID.
func Retry(ctx context.Context, f func() error) error {
	err := f()
	if connect.CodeOf(err) == connect.CodeUnauthenticated && ctx.Err() == nil {
		err = f()
	}
	return err
}
