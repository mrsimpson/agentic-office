package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/client/replicator/locallog"
	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	tenantv1 "github.com/mrsimpson/agentic-office/hub/tenant/api"
)

// joinState is the content of the join state file.
type joinState struct {
	SessionID     string `json:"sessionId"`
	JoinCommandID string `json:"joinCommandId"`
}

// Join adds the participant of the key to a session. It keeps the join command ID in the join
// state file and reuses it, so a restarted client is the same participant
// (dec-idempotent-commands). It does not start the replicator.
func (c *hubClient) Join(ctx context.Context, sessionID string, opts coreapi.JoinOptions) (*coreapi.Joined, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	paths, err := c.cfg.Paths(sessionID, pid)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		st, err := loadOrCreateJoinState(paths.Join, sessionID)
		if err != nil {
			return nil, err
		}
		var res *sessioncommandsv1.JoinResponse
		err = retry(ctx, func() (err error) {
			res, err = c.commands.Join(ctx, &sessioncommandsv1.JoinRequest{SessionId: sessionID, CommandId: st.JoinCommandID,
				DisplayName: opts.DisplayName, Captured: opts.Captured})
			return err
		})
		if err != nil {
			return nil, err
		}
		// A stored join command ID returns the old join also after the participant has left (for
		// example from another client). Then the state is stale: join again with a new ID.
		if res.GetResult().GetDuplicate() && attempt == 0 && leftAfter(paths.Log, pid, res.GetResult().GetEvent().GetId()) {
			if err := os.Remove(paths.Join); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			continue
		}
		return &coreapi.Joined{SessionID: sessionID, ParticipantID: res.GetParticipantId(), Paths: paths, Result: res.GetResult()}, nil
	}
}

// leftAfter reports whether the local log shows that pid left after the event joinID.
func leftAfter(logPath, pid string, joinID uint32) bool {
	evs, err := locallog.Read(logPath)
	if err != nil {
		return false
	}
	for _, ev := range evs {
		if ev.GetId() > joinID && ev.GetType() == events.ParticipantLeft && ev.GetParticipantLeft().GetParticipantId() == pid {
			return true
		}
	}
	return false
}

func loadOrCreateJoinState(path, sessionID string) (joinState, error) {
	var st joinState
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &st); err == nil && st.JoinCommandID != "" && st.SessionID == sessionID {
			return st, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return st, err
	}
	st = joinState{SessionID: sessionID, JoinCommandID: coreapi.NewCommandID()}
	b, _ := json.Marshal(st)
	// Write before the join is sent, so a crash right after the send keeps the ID.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return st, err
	}
	return st, os.Rename(tmp, path)
}

// joinedSessions returns the sessions that have a join state for participantID in the runtime
// directory, that is the sessions this participant joined from this machine and did not leave.
// Sessions whose local log ends with session.ended are left out.
func joinedSessions(c coreapi.Config, participantID string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(c.RuntimeDir, "*", coreapi.FileName(participantID)+".join"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range matches {
		var st joinState
		if b, err := os.ReadFile(m); err == nil && json.Unmarshal(b, &st) == nil && st.SessionID != "" &&
			!endedLog(strings.TrimSuffix(m, ".join")+".jsonl") {
			out = append(out, st.SessionID)
		}
	}
	return out, nil
}

// DefaultSession returns the only session that participantID has joined from this machine.
func (c *hubClient) DefaultSession(ctx context.Context) (string, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return "", err
	}
	ss, err := joinedSessions(c.cfg, pid)
	if err != nil {
		return "", err
	}
	switch len(ss) {
	case 0:
		return "", fmt.Errorf("%w: %s has not joined a session on this machine; pass --session", coreapi.ErrNoSession, pid)
	case 1:
		return ss[0], nil
	}
	return "", fmt.Errorf("%w: %s has joined several sessions (%v); pass --session", coreapi.ErrNoSession, pid, ss)
}

// command runs one session command with a new command ID. call gets the participant and the
// command ID and returns the result.
func (c *hubClient) command(ctx context.Context, call func(pid, cmdID string) (*sessioncommandsv1.CommandResult, error)) (*sessioncommandsv1.CommandResult, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	cmdID := coreapi.NewCommandID()
	var res *sessioncommandsv1.CommandResult
	err = retry(ctx, func() (err error) {
		res, err = call(pid, cmdID)
		return err
	})
	return res, err
}

// Leave removes the participant from the session and deletes its join state. The replicator
// stops by itself when it has written the participant.left event.
func (c *hubClient) Leave(ctx context.Context, sessionID string) (*sessioncommandsv1.CommandResult, error) {
	res, err := c.command(ctx, func(pid, id string) (*sessioncommandsv1.CommandResult, error) {
		r, err := c.commands.Leave(ctx, &sessioncommandsv1.LeaveRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid})
		return r.GetResult(), err
	})
	if err != nil {
		return nil, err
	}
	pid, _ := c.ParticipantID(ctx)
	if p, err := c.cfg.Paths(sessionID, pid); err == nil {
		if err := os.Remove(p.Join); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return res, err
		}
	}
	return res, nil
}

// Say submits a typed utterance (modality text).
func (c *hubClient) Say(ctx context.Context, sessionID, text string) (*sessioncommandsv1.CommandResult, error) {
	return c.SubmitUtterance(ctx, sessionID, coreapi.NewCommandID(), text, sessionstreamv1.Modality_MODALITY_TEXT, time.Time{})
}

// SubmitUtterance submits an utterance with a given command ID. The ears (#8) use it with the
// ID they created when the sentence became final, so a resend after a drop is not stored twice.
// transcribedAt may be zero for typed text.
func (c *hubClient) SubmitUtterance(ctx context.Context, sessionID, cmdID, text string, modality sessionstreamv1.Modality, transcribedAt time.Time) (*sessioncommandsv1.CommandResult, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req := &sessioncommandsv1.SubmitUtteranceRequest{SessionId: sessionID, CommandId: cmdID, ParticipantId: pid, Text: text, Modality: modality}
	if !transcribedAt.IsZero() {
		req.TranscribedAt = timestamppb.New(transcribedAt)
	}
	var res *sessioncommandsv1.CommandResult
	err = retry(ctx, func() error {
		r, err := c.commands.SubmitUtterance(ctx, req)
		res = r.GetResult()
		return err
	})
	return res, err
}

// MarkOffTheRecord marks an own utterance off the record by its event ID (dec-off-the-record).
func (c *hubClient) MarkOffTheRecord(ctx context.Context, sessionID string, utteranceEventID uint32) (*sessioncommandsv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessioncommandsv1.CommandResult, error) {
		r, err := c.commands.MarkOffTheRecord(ctx, &sessioncommandsv1.MarkOffTheRecordRequest{SessionId: sessionID, CommandId: id,
			ParticipantId: pid, UtteranceEventId: utteranceEventID})
		return r.GetResult(), err
	})
}

// TurnEnd submits a turn.end signal for participantID (empty: the caller).
func (c *hubClient) TurnEnd(ctx context.Context, sessionID, participantID string) (*sessioncommandsv1.CommandResult, error) {
	return c.SubmitTurnEnd(ctx, sessionID, coreapi.NewCommandID(), participantID)
}

// SubmitTurnEnd submits a turn.end signal with a given command ID. The ears use it with the ID
// they created when the turn ended, so a resend after a drop is not stored twice.
func (c *hubClient) SubmitTurnEnd(ctx context.Context, sessionID, cmdID, participantID string) (*sessioncommandsv1.CommandResult, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	if participantID == "" {
		participantID = pid
	}
	req := &sessioncommandsv1.SubmitSignalRequest{SessionId: sessionID, CommandId: cmdID, ParticipantId: pid,
		Signal: &sessioncommandsv1.SubmitSignalRequest_TurnEnd{TurnEnd: &sessionstreamv1.TurnEnd{ParticipantId: participantID}}}
	var res *sessioncommandsv1.CommandResult
	err = retry(ctx, func() error {
		r, err := c.commands.SubmitSignal(ctx, req)
		res = r.GetResult()
		return err
	})
	return res, err
}

// SilenceLong submits a silence.long signal.
func (c *hubClient) SilenceLong(ctx context.Context, sessionID string, d time.Duration) (*sessioncommandsv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessioncommandsv1.CommandResult, error) {
		r, err := c.commands.SubmitSignal(ctx, &sessioncommandsv1.SubmitSignalRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid,
			Signal: &sessioncommandsv1.SubmitSignalRequest_SilenceLong{SilenceLong: &sessionstreamv1.SilenceLong{Duration: durationpb.New(d)}}})
		return r.GetResult(), err
	})
}

// Addressed submits an addressed signal: utteranceEventID mentioned participantID.
func (c *hubClient) Addressed(ctx context.Context, sessionID, participantID string, utteranceEventID uint32) (*sessioncommandsv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessioncommandsv1.CommandResult, error) {
		r, err := c.commands.SubmitSignal(ctx, &sessioncommandsv1.SubmitSignalRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid,
			Signal: &sessioncommandsv1.SubmitSignalRequest_Addressed{Addressed: &sessionstreamv1.Addressed{ParticipantId: participantID,
				UtteranceEventId: utteranceEventID}}})
		return r.GetResult(), err
	})
}

// Pause pauses the transcription of the participant.
func (c *hubClient) Pause(ctx context.Context, sessionID string) (*sessioncommandsv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessioncommandsv1.CommandResult, error) {
		r, err := c.commands.Pause(ctx, &sessioncommandsv1.PauseRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid})
		return r.GetResult(), err
	})
}

// Resume ends a pause.
func (c *hubClient) Resume(ctx context.Context, sessionID string) (*sessioncommandsv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessioncommandsv1.CommandResult, error) {
		r, err := c.commands.Resume(ctx, &sessioncommandsv1.ResumeRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid})
		return r.GetResult(), err
	})
}

// RaiseHand raises the hand of the participant, optionally with a topic.
func (c *hubClient) RaiseHand(ctx context.Context, sessionID, topic string) (*sessioncommandsv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessioncommandsv1.CommandResult, error) {
		r, err := c.commands.RaiseHand(ctx, &sessioncommandsv1.RaiseHandRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid, Topic: topic})
		return r.GetResult(), err
	})
}

// UpdateMinutes replaces the running minutes with text.
func (c *hubClient) UpdateMinutes(ctx context.Context, sessionID, text string) (*sessioncommandsv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessioncommandsv1.CommandResult, error) {
		r, err := c.commands.UpdateMinutes(ctx, &sessioncommandsv1.UpdateMinutesRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid, Text: text})
		return r.GetResult(), err
	})
}

// FocusArtifact shows an artifact on the stage.
func (c *hubClient) FocusArtifact(ctx context.Context, sessionID, artifactID string) (*sessioncommandsv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessioncommandsv1.CommandResult, error) {
		r, err := c.commands.FocusArtifact(ctx, &sessioncommandsv1.FocusArtifactRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid, ArtifactId: artifactID})
		return r.GetResult(), err
	})
}

// EndSession ends the session for everyone.
func (c *hubClient) EndSession(ctx context.Context, sessionID string) (*sessioncommandsv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessioncommandsv1.CommandResult, error) {
		r, err := c.commands.EndSession(ctx, &sessioncommandsv1.EndSessionRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid})
		return r.GetResult(), err
	})
}

// StageLink creates a read-only link to the stage of a session.
func (c *hubClient) StageLink(ctx context.Context, sessionID string) (*identityv1.CreateStageLinkResponse, error) {
	cmdID := coreapi.NewCommandID()
	var res *identityv1.CreateStageLinkResponse
	err := retry(ctx, func() (err error) {
		res, err = c.identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{CommandId: cmdID, SessionId: sessionID})
		return err
	})
	return res, err
}

// CreateUser registers a user with a first key. Only a system admin may call it.
func (c *hubClient) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
	req.CommandId = coreapi.NewCommandID()
	var res *userv1.CreateUserResponse
	err := retry(ctx, func() (err error) {
		res, err = c.users.CreateUser(ctx, req)
		return err
	})
	return res, err
}

// AddKey adds a public key to a user. A user adds keys for itself, a system admin for anyone.
func (c *hubClient) AddKey(ctx context.Context, userID string, publicKey []byte) (string, error) {
	req := &userv1.AddKeyRequest{CommandId: coreapi.NewCommandID(), UserId: userID, PublicKey: publicKey}
	var res *userv1.AddKeyResponse
	err := retry(ctx, func() (err error) {
		res, err = c.users.AddKey(ctx, req)
		return err
	})
	return res.GetKeyId(), err
}

// RevokeKey revokes a key by its key ID. A user revokes its own keys, a system admin any key.
func (c *hubClient) RevokeKey(ctx context.Context, keyID string) error {
	req := &userv1.RevokeKeyRequest{CommandId: coreapi.NewCommandID(), KeyId: keyID}
	return retry(ctx, func() error {
		_, err := c.users.RevokeKey(ctx, req)
		return err
	})
}

// ListUsers returns all users, also the removed ones. Only a system admin may call it.
func (c *hubClient) ListUsers(ctx context.Context) ([]*userv1.User, error) {
	var res *userv1.ListUsersResponse
	err := retry(ctx, func() (err error) {
		res, err = c.users.ListUsers(ctx, &userv1.ListUsersRequest{})
		return err
	})
	return res.GetUsers(), err
}

// RemoveUser ends a user and its memberships. Only a system admin may call it.
func (c *hubClient) RemoveUser(ctx context.Context, userID string) error {
	req := &userv1.RemoveUserRequest{CommandId: coreapi.NewCommandID(), UserId: userID}
	return retry(ctx, func() error {
		_, err := c.users.RemoveUser(ctx, req)
		return err
	})
}

// SetSystemAdmin makes a human user a system admin, or takes the role away.
func (c *hubClient) SetSystemAdmin(ctx context.Context, userID string, on bool) error {
	req := &userv1.SetSystemAdminRequest{CommandId: coreapi.NewCommandID(), UserId: userID, SystemAdmin: on}
	return retry(ctx, func() error {
		_, err := c.users.SetSystemAdmin(ctx, req)
		return err
	})
}

// SetMembership adds a user to a tenant with a role, or changes the role.
func (c *hubClient) SetMembership(ctx context.Context, tenantID, userID string, role userv1.Role) error {
	req := &userv1.SetMembershipRequest{CommandId: coreapi.NewCommandID(), TenantId: tenantID, UserId: userID, Role: role}
	return retry(ctx, func() error {
		_, err := c.users.SetMembership(ctx, req)
		return err
	})
}

// RemoveMembership ends the membership of a user in a tenant.
func (c *hubClient) RemoveMembership(ctx context.Context, tenantID, userID string) error {
	req := &userv1.RemoveMembershipRequest{CommandId: coreapi.NewCommandID(), TenantId: tenantID, UserId: userID}
	return retry(ctx, func() error {
		_, err := c.users.RemoveMembership(ctx, req)
		return err
	})
}

// ListMemberships returns the memberships of a tenant, also the ended ones.
func (c *hubClient) ListMemberships(ctx context.Context, tenantID string) ([]*userv1.Membership, error) {
	var res *userv1.ListMembershipsResponse
	err := retry(ctx, func() (err error) {
		res, err = c.users.ListMemberships(ctx, &userv1.ListMembershipsRequest{TenantId: tenantID})
		return err
	})
	return res.GetMemberships(), err
}

// CreateTenant creates a tenant with its first tenant admin. Only a system admin may call it.
func (c *hubClient) CreateTenant(ctx context.Context, tenantID, displayName, firstAdminUserID string) error {
	req := &tenantv1.CreateTenantRequest{CommandId: coreapi.NewCommandID(), TenantId: tenantID, DisplayName: displayName,
		FirstAdminUserId: firstAdminUserID}
	return retry(ctx, func() error {
		_, err := c.tenants.CreateTenant(ctx, req)
		return err
	})
}

// RemoveTenant ends a tenant and its memberships. Only a system admin may call it.
func (c *hubClient) RemoveTenant(ctx context.Context, tenantID string) error {
	req := &tenantv1.RemoveTenantRequest{CommandId: coreapi.NewCommandID(), TenantId: tenantID}
	return retry(ctx, func() error {
		_, err := c.tenants.RemoveTenant(ctx, req)
		return err
	})
}

// ListTenants returns all tenants, also the removed ones. Only a system admin may call it.
func (c *hubClient) ListTenants(ctx context.Context) ([]*tenantv1.Tenant, error) {
	var res *tenantv1.ListTenantsResponse
	err := retry(ctx, func() (err error) {
		res, err = c.tenants.ListTenants(ctx, &tenantv1.ListTenantsRequest{})
		return err
	})
	return res.GetTenants(), err
}

// RenderDeck renders a slide deck. The author is the caller.
func (c *hubClient) RenderDeck(ctx context.Context, req *slidesv1.RenderDeckRequest) (*slidesv1.RenderDeckResponse, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req.CommandId, req.AuthorParticipantId = coreapi.NewCommandID(), pid
	var res *slidesv1.RenderDeckResponse
	err = retry(ctx, func() (err error) {
		res, err = c.slides.RenderDeck(ctx, req)
		return err
	})
	return res, err
}

// GetDeck loads a slide deck with its input and its rendered output by its ID, for example
// "slides/42".
func (c *hubClient) GetDeck(ctx context.Context, id string) (*slidesv1.Deck, error) {
	var res *slidesv1.GetDeckResponse
	err := retry(ctx, func() (err error) {
		res, err = c.slides.GetDeck(ctx, &slidesv1.GetDeckRequest{Id: id})
		return err
	})
	return res.GetDeck(), err
}

// CreateAdminLink creates a link to the admin view.
func (c *hubClient) CreateAdminLink(ctx context.Context) (*identityv1.CreateAdminLinkResponse, error) {
	req := &identityv1.CreateAdminLinkRequest{CommandId: coreapi.NewCommandID()}
	var res *identityv1.CreateAdminLinkResponse
	err := retry(ctx, func() (err error) {
		res, err = c.identity.CreateAdminLink(ctx, req)
		return err
	})
	return res, err
}

// CreateDocument creates a text document. The author is the caller.
func (c *hubClient) CreateDocument(ctx context.Context, path, content, sessionID string) (*documentv1.CreateDocumentResponse, error) {
	req := &documentv1.CreateDocumentRequest{Path: path, Content: content, SessionId: sessionID, CommandId: coreapi.NewCommandID()}
	var res *documentv1.CreateDocumentResponse
	err := retry(ctx, func() (err error) {
		res, err = c.document.CreateDocument(ctx, req)
		return err
	})
	return res, err
}

// ChangeDocument replaces a text document, based on the version that the caller read.
func (c *hubClient) ChangeDocument(ctx context.Context, path, content, baseVersion, sessionID string) (*documentv1.ChangeDocumentResponse, error) {
	req := &documentv1.ChangeDocumentRequest{Path: path, Content: content, BaseVersion: baseVersion, SessionId: sessionID,
		CommandId: coreapi.NewCommandID()}
	var res *documentv1.ChangeDocumentResponse
	err := retry(ctx, func() (err error) {
		res, err = c.document.ChangeDocument(ctx, req)
		return err
	})
	return res, err
}

// ReadFile reads a file of the archive by its path.
func (c *hubClient) ReadFile(ctx context.Context, path string) (*archivev1.File, error) {
	var res *archivev1.ReadFileResponse
	err := retry(ctx, func() (err error) {
		res, err = c.archive.ReadFile(ctx, &archivev1.ReadFileRequest{Path: path})
		return err
	})
	return res.GetFile(), err
}

// ListFiles lists the files of a directory of the archive.
func (c *hubClient) ListFiles(ctx context.Context, dir string) ([]*archivev1.FileInfo, error) {
	var res *archivev1.ListFilesResponse
	err := retry(ctx, func() (err error) {
		res, err = c.archive.ListFiles(ctx, &archivev1.ListFilesRequest{Dir: dir})
		return err
	})
	return res.GetFiles(), err
}

// RecordDecision records a decision. The author is the caller. sessionID may be empty.
func (c *hubClient) RecordDecision(ctx context.Context, text, sessionID string) (*decisionv1.RecordDecisionResponse, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req := &decisionv1.RecordDecisionRequest{CommandId: coreapi.NewCommandID(), Text: text, AuthorParticipantId: pid, SessionId: sessionID}
	var res *decisionv1.RecordDecisionResponse
	err = retry(ctx, func() (err error) {
		res, err = c.decision.RecordDecision(ctx, req)
		return err
	})
	return res, err
}

// ListDecisions lists decisions after a cursor. sessionID may be empty (all); limit 0 is the
// default of the hub.
func (c *hubClient) ListDecisions(ctx context.Context, after uint32, sessionID string, limit uint32) ([]*decisionv1.Decision, error) {
	var res *decisionv1.ListDecisionsResponse
	err := retry(ctx, func() (err error) {
		res, err = c.decision.ListDecisions(ctx, &decisionv1.ListDecisionsRequest{AfterId: after, SessionId: sessionID, Limit: limit})
		return err
	})
	return res.GetDecisions(), err
}

// AddNote adds a note. The author is the caller. sessionID may be empty.
func (c *hubClient) AddNote(ctx context.Context, text, sessionID string) (*notev1.AddNoteResponse, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req := &notev1.AddNoteRequest{CommandId: coreapi.NewCommandID(), Text: text, AuthorParticipantId: pid, SessionId: sessionID}
	var res *notev1.AddNoteResponse
	err = retry(ctx, func() (err error) {
		res, err = c.note.AddNote(ctx, req)
		return err
	})
	return res, err
}

// ListNotes lists notes after a cursor. sessionID may be empty (all); limit 0 is the default of
// the hub.
func (c *hubClient) ListNotes(ctx context.Context, after uint32, sessionID string, limit uint32) ([]*notev1.Note, error) {
	var res *notev1.ListNotesResponse
	err := retry(ctx, func() (err error) {
		res, err = c.note.ListNotes(ctx, &notev1.ListNotesRequest{AfterId: after, SessionId: sessionID, Limit: limit})
		return err
	})
	return res.GetNotes(), err
}

// GetMinutes returns the stored minutes of an ended session.
func (c *hubClient) GetMinutes(ctx context.Context, sessionID string) (*minutesv1.Minutes, error) {
	var res *minutesv1.GetMinutesResponse
	err := retry(ctx, func() (err error) {
		res, err = c.minutes.GetMinutes(ctx, &minutesv1.GetMinutesRequest{SessionId: sessionID})
		return err
	})
	return res.GetMinutes(), err
}

// ListMinutes lists stored minutes after a cursor. sessionID may be empty (all); limit 0 is the
// default of the hub.
func (c *hubClient) ListMinutes(ctx context.Context, after uint32, sessionID string, limit uint32) ([]*minutesv1.Minutes, error) {
	var res *minutesv1.ListMinutesResponse
	err := retry(ctx, func() (err error) {
		res, err = c.minutes.ListMinutes(ctx, &minutesv1.ListMinutesRequest{AfterId: after, SessionId: sessionID, Limit: limit})
		return err
	})
	return res.GetMinutes(), err
}

// Events returns the events of a session after a cursor, at most limit of them (0: the server's
// default), and whether there are more (dec-events-from-cursor). Event IDs have no gaps, so a
// participant that finds a gap in its copy reads the missing events here.
func (c *hubClient) Events(ctx context.Context, sessionID string, after, limit uint32) ([]*sessionstreamv1.Event, bool, error) {
	var res *sessionstreamv1.GetEventsResponse
	err := retry(ctx, func() (err error) {
		res, err = c.stream.GetEvents(ctx, &sessionstreamv1.GetEventsRequest{SessionId: sessionID, AfterEventId: after, Limit: limit})
		return err
	})
	return res.GetEvents(), res.GetMore(), err
}

// endedLog reports whether the local log at path ends with session.ended.
func endedLog(path string) bool {
	evs, err := locallog.Read(path)
	return err == nil && len(evs) > 0 && evs[len(evs)-1].GetType() == events.SessionEnded
}
