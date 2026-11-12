package coreapi

import (
	"context"
	"errors"
	"time"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/hub/session/commandsapi/sessioncommandsv1connect"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/sessionstreamv1connect"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
	tenantv1 "github.com/mrsimpson/agentic-office/hub/tenant/api"
)

// Dial returns an authenticated client of the office hub for the key of cfg. It does not connect
// yet; the first call authenticates (rs-authenticate). It fails with ErrNoKey if the key file
// does not exist. The client core provides it; the wiring of the participant client hands it to
// the adapters.
type Dial func(cfg Config) (Client, error)

// ErrNoKey means that the key file does not exist.
var ErrNoKey = errors.New("no key file")

// ErrNoSession means that no session was given and none or several are joined.
var ErrNoSession = errors.New("no session")

// JoinOptions are the optional fields of a join.
type JoinOptions struct {
	// DisplayName is the name in this session. Empty means the registered name.
	DisplayName string
	// Captured is true if the participant's speech is transcribed (--mic).
	Captured bool
}

// Joined is the result of Join.
type Joined struct {
	SessionID     string
	ParticipantID string
	Paths         Paths
	Result        *sessioncommandsv1.CommandResult
}

// Client is an authenticated client of the office hub for one actor (one key). Every session
// command creates its own command ID and is repeated once if the token was rejected
// (dec-idempotent-commands).
type Client interface {
	// Config returns the configuration the client was dialled with.
	Config() Config
	// KeyID returns the ID of the public key of the actor.
	KeyID() string
	// ParticipantID authenticates if needed and returns the participant of the key.
	ParticipantID(ctx context.Context) (string, error)

	// Commands returns the session commands client, for example for a replicator.
	Commands() sessioncommandsv1connect.SessionCommandsServiceClient
	// Stream returns the session stream client, for example for a replicator.
	Stream() sessionstreamv1connect.SessionStreamServiceClient

	// Join adds the participant of the key to a session. It keeps the join command ID in the
	// join state file and reuses it, so a restarted client is the same participant. It does not
	// start the replicator.
	Join(ctx context.Context, sessionID string, opts JoinOptions) (*Joined, error)
	// DefaultSession returns the only session that the participant has joined from this
	// machine, or ErrNoSession.
	DefaultSession(ctx context.Context) (string, error)
	// Leave removes the participant from the session and deletes its join state.
	Leave(ctx context.Context, sessionID string) (*sessioncommandsv1.CommandResult, error)
	// Say submits a typed utterance.
	Say(ctx context.Context, sessionID, text string) (*sessioncommandsv1.CommandResult, error)
	// SubmitUtterance submits an utterance with a command ID that the caller created.
	SubmitUtterance(ctx context.Context, sessionID, cmdID, text string, modality sessionstreamv1.Modality,
		transcribedAt time.Time) (*sessioncommandsv1.CommandResult, error)
	MarkOffTheRecord(ctx context.Context, sessionID string, utteranceEventID uint32) (*sessioncommandsv1.CommandResult, error)
	// MarkOffTheRecordLast marks off the record the newest own sentence that is not off the
	// record yet, or fails with ErrNothingOffTheRecord. It reads the local log.
	MarkOffTheRecordLast(ctx context.Context, sessionID string) (*sessioncommandsv1.CommandResult, error)
	// TurnEnd submits a turn.end signal for participantID (empty: the caller).
	TurnEnd(ctx context.Context, sessionID, participantID string) (*sessioncommandsv1.CommandResult, error)
	// SubmitTurnEnd is TurnEnd with a command ID that the caller created.
	SubmitTurnEnd(ctx context.Context, sessionID, cmdID, participantID string) (*sessioncommandsv1.CommandResult, error)
	SilenceLong(ctx context.Context, sessionID string, d time.Duration) (*sessioncommandsv1.CommandResult, error)
	Addressed(ctx context.Context, sessionID, participantID string, utteranceEventID uint32) (*sessioncommandsv1.CommandResult, error)
	Pause(ctx context.Context, sessionID string) (*sessioncommandsv1.CommandResult, error)
	Resume(ctx context.Context, sessionID string) (*sessioncommandsv1.CommandResult, error)
	RaiseHand(ctx context.Context, sessionID, topic string) (*sessioncommandsv1.CommandResult, error)
	UpdateMinutes(ctx context.Context, sessionID, text string) (*sessioncommandsv1.CommandResult, error)
	ShowArtifact(ctx context.Context, sessionID, artifactID string) (*sessioncommandsv1.CommandResult, error)
	EndSession(ctx context.Context, sessionID string) (*sessioncommandsv1.CommandResult, error)
	// Events returns the events after a cursor from the hub, and whether there are more.
	Events(ctx context.Context, sessionID string, after, limit uint32) ([]*sessionstreamv1.Event, bool, error)

	// Import imports a conversation as a new session (dec-session-import). sessionID may be
	// empty: then Import creates one.
	Import(ctx context.Context, sessionID string, conv *Conversation) (*Imported, error)

	StageLink(ctx context.Context, sessionID string) (*identityv1.CreateStageLinkResponse, error)
	// CreateUser registers a user with a first key (if-user-api). Only a system admin may call it.
	CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error)
	// AddKey adds a key to a user: for itself, or for anyone as a system admin.
	AddKey(ctx context.Context, userID string, publicKey []byte) (string, error)
	// RevokeKey revokes a key by its key ID: an own key, or any key as a system admin.
	RevokeKey(ctx context.Context, keyID string) error

	// ListUsers returns all users, also the removed ones. Only a system admin may call it.
	ListUsers(ctx context.Context) ([]*userv1.User, error)
	// RemoveUser ends a user and its memberships. Only a system admin may call it.
	RemoveUser(ctx context.Context, userID string) error
	// SetSystemAdmin makes a human user a system admin, or takes the role away. Only a system
	// admin may call it.
	SetSystemAdmin(ctx context.Context, userID string, on bool) error
	// SetMembership adds a user to a tenant with a role, or changes the role. A system admin or the
	// tenant admin of the tenant may call it.
	SetMembership(ctx context.Context, tenantID, userID string, role userv1.Role) error
	// RemoveMembership ends the membership of a user in a tenant. Who may: as for SetMembership.
	RemoveMembership(ctx context.Context, tenantID, userID string) error
	// ListMemberships returns the memberships of a tenant, also the ended ones. A system admin or a
	// member of the tenant may call it.
	ListMemberships(ctx context.Context, tenantID string) ([]*userv1.Membership, error)

	// CreateTenant, RemoveTenant and ListTenants manage the tenants of the hub (if-tenant-api).
	// Only a system admin may call them.
	CreateTenant(ctx context.Context, tenantID, displayName, firstAdminUserID string) error
	RemoveTenant(ctx context.Context, tenantID string) error
	ListTenants(ctx context.Context) ([]*tenantv1.Tenant, error)

	RenderDeck(ctx context.Context, req *slidesv1.RenderDeckRequest) (*slidesv1.RenderDeckResponse, error)
	GetDeck(ctx context.Context, id string) (*slidesv1.Deck, error)

	RecordDecision(ctx context.Context, text, sessionID string) (*decisionv1.RecordDecisionResponse, error)
	ListDecisions(ctx context.Context, after uint32, sessionID string, limit uint32) ([]*decisionv1.Decision, error)
	AddNote(ctx context.Context, text, sessionID string) (*notev1.AddNoteResponse, error)
	ListNotes(ctx context.Context, after uint32, sessionID string, limit uint32) ([]*notev1.Note, error)
	GetMinutes(ctx context.Context, sessionID string) (*minutesv1.Minutes, error)
	ListMinutes(ctx context.Context, after uint32, sessionID string, limit uint32) ([]*minutesv1.Minutes, error)

	CreateTask(ctx context.Context, d TaskDraft) (*taskv1.CreateTaskResponse, error)
	GetTask(ctx context.Context, ref string) (*taskv1.Task, error)
	ListTasks(ctx context.Context, f TaskFilter) ([]*taskv1.Task, string, error)
	UpdateTask(ctx context.Context, ref string, ch TaskChange) (*taskv1.UpdateTaskResponse, error)
}
