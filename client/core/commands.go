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

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/hub/session/api/events"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
)

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
	Result        *sessionv1.CommandResult
}

// joinState is the content of the join state file.
type joinState struct {
	SessionID     string `json:"sessionId"`
	JoinCommandID string `json:"joinCommandId"`
}

// Join adds the participant of the key to a session. It keeps the join command ID in the join
// state file and reuses it, so a restarted client is the same participant
// (dec-idempotent-commands). It does not start the replicator.
func (c *Client) Join(ctx context.Context, sessionID string, opts JoinOptions) (*Joined, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	paths, err := c.Paths(sessionID, pid)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		st, err := loadOrCreateJoinState(paths.Join, sessionID)
		if err != nil {
			return nil, err
		}
		var res *sessionv1.JoinResponse
		err = Retry(ctx, func() (err error) {
			res, err = c.Session.Join(ctx, &sessionv1.JoinRequest{SessionId: sessionID, CommandId: st.JoinCommandID,
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
		return &Joined{SessionID: sessionID, ParticipantID: res.GetParticipantId(), Paths: paths, Result: res.GetResult()}, nil
	}
}

// leftAfter reports whether the local log shows that pid left after the event joinID.
func leftAfter(logPath, pid string, joinID uint32) bool {
	evs, err := ReadLog(logPath)
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
	st = joinState{SessionID: sessionID, JoinCommandID: ids.NewCommandID()}
	b, _ := json.Marshal(st)
	// Write before the join is sent, so a crash right after the send keeps the ID.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return st, err
	}
	return st, os.Rename(tmp, path)
}

// JoinedSessions returns the sessions that have a join state for participantID in the runtime
// directory, that is the sessions this participant joined from this machine and did not leave.
// Sessions whose local log ends with session.ended are left out.
func (c Config) JoinedSessions(participantID string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(c.RuntimeDir, "*", FileName(participantID)+".join"))
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

// ErrNoSession means that no session was given and none or several are joined.
var ErrNoSession = errors.New("no session")

// DefaultSession returns the only session that participantID has joined from this machine.
func (c *Client) DefaultSession(ctx context.Context) (string, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return "", err
	}
	ss, err := c.JoinedSessions(pid)
	if err != nil {
		return "", err
	}
	switch len(ss) {
	case 0:
		return "", fmt.Errorf("%w: %s has not joined a session on this machine; pass --session", ErrNoSession, pid)
	case 1:
		return ss[0], nil
	}
	return "", fmt.Errorf("%w: %s has joined several sessions (%v); pass --session", ErrNoSession, pid, ss)
}

// command runs one session command with a new command ID. call gets the participant and the
// command ID and returns the result.
func (c *Client) command(ctx context.Context, call func(pid, cmdID string) (*sessionv1.CommandResult, error)) (*sessionv1.CommandResult, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	cmdID := ids.NewCommandID()
	var res *sessionv1.CommandResult
	err = Retry(ctx, func() (err error) {
		res, err = call(pid, cmdID)
		return err
	})
	return res, err
}

// Leave removes the participant from the session and deletes its join state. The replicator
// stops by itself when it has written the participant.left event.
func (c *Client) Leave(ctx context.Context, sessionID string) (*sessionv1.CommandResult, error) {
	res, err := c.command(ctx, func(pid, id string) (*sessionv1.CommandResult, error) {
		r, err := c.Session.Leave(ctx, &sessionv1.LeaveRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid})
		return r.GetResult(), err
	})
	if err != nil {
		return nil, err
	}
	pid, _ := c.ParticipantID(ctx)
	if p, err := c.Paths(sessionID, pid); err == nil {
		if err := os.Remove(p.Join); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return res, err
		}
	}
	return res, nil
}

// Say submits a typed utterance (modality text).
func (c *Client) Say(ctx context.Context, sessionID, text string) (*sessionv1.CommandResult, error) {
	return c.SubmitUtterance(ctx, sessionID, ids.NewCommandID(), text, sessionv1.Modality_MODALITY_TEXT, time.Time{})
}

// SubmitUtterance submits an utterance with a given command ID. The ears (#8) use it with the
// ID they created when the sentence became final, so a resend after a drop is not stored twice.
// transcribedAt may be zero for typed text.
func (c *Client) SubmitUtterance(ctx context.Context, sessionID, cmdID, text string, modality sessionv1.Modality, transcribedAt time.Time) (*sessionv1.CommandResult, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req := &sessionv1.SubmitUtteranceRequest{SessionId: sessionID, CommandId: cmdID, ParticipantId: pid, Text: text, Modality: modality}
	if !transcribedAt.IsZero() {
		req.TranscribedAt = timestamppb.New(transcribedAt)
	}
	var res *sessionv1.CommandResult
	err = Retry(ctx, func() error {
		r, err := c.Session.SubmitUtterance(ctx, req)
		res = r.GetResult()
		return err
	})
	return res, err
}

// MarkOffTheRecord marks an own utterance off the record by its event ID (dec-off-the-record).
func (c *Client) MarkOffTheRecord(ctx context.Context, sessionID string, utteranceEventID uint32) (*sessionv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessionv1.CommandResult, error) {
		r, err := c.Session.MarkOffTheRecord(ctx, &sessionv1.MarkOffTheRecordRequest{SessionId: sessionID, CommandId: id,
			ParticipantId: pid, UtteranceEventId: utteranceEventID})
		return r.GetResult(), err
	})
}

// TurnEnd submits a turn.end signal for participantID (empty: the caller).
func (c *Client) TurnEnd(ctx context.Context, sessionID, participantID string) (*sessionv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessionv1.CommandResult, error) {
		if participantID == "" {
			participantID = pid
		}
		r, err := c.Session.SubmitSignal(ctx, &sessionv1.SubmitSignalRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid,
			Signal: &sessionv1.SubmitSignalRequest_TurnEnd{TurnEnd: &sessionv1.TurnEnd{ParticipantId: participantID}}})
		return r.GetResult(), err
	})
}

// SilenceLong submits a silence.long signal.
func (c *Client) SilenceLong(ctx context.Context, sessionID string, d time.Duration) (*sessionv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessionv1.CommandResult, error) {
		r, err := c.Session.SubmitSignal(ctx, &sessionv1.SubmitSignalRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid,
			Signal: &sessionv1.SubmitSignalRequest_SilenceLong{SilenceLong: &sessionv1.SilenceLong{Duration: durationpb.New(d)}}})
		return r.GetResult(), err
	})
}

// Addressed submits an addressed signal: utteranceEventID mentioned participantID.
func (c *Client) Addressed(ctx context.Context, sessionID, participantID string, utteranceEventID uint32) (*sessionv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessionv1.CommandResult, error) {
		r, err := c.Session.SubmitSignal(ctx, &sessionv1.SubmitSignalRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid,
			Signal: &sessionv1.SubmitSignalRequest_Addressed{Addressed: &sessionv1.Addressed{ParticipantId: participantID,
				UtteranceEventId: utteranceEventID}}})
		return r.GetResult(), err
	})
}

// Pause pauses the transcription of the participant.
func (c *Client) Pause(ctx context.Context, sessionID string) (*sessionv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessionv1.CommandResult, error) {
		r, err := c.Session.Pause(ctx, &sessionv1.PauseRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid})
		return r.GetResult(), err
	})
}

// Resume ends a pause.
func (c *Client) Resume(ctx context.Context, sessionID string) (*sessionv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessionv1.CommandResult, error) {
		r, err := c.Session.Resume(ctx, &sessionv1.ResumeRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid})
		return r.GetResult(), err
	})
}

// RaiseHand raises the hand of the participant, optionally with a topic.
func (c *Client) RaiseHand(ctx context.Context, sessionID, topic string) (*sessionv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessionv1.CommandResult, error) {
		r, err := c.Session.RaiseHand(ctx, &sessionv1.RaiseHandRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid, Topic: topic})
		return r.GetResult(), err
	})
}

// UpdateMinutes replaces the running minutes with text.
func (c *Client) UpdateMinutes(ctx context.Context, sessionID, text string) (*sessionv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessionv1.CommandResult, error) {
		r, err := c.Session.UpdateMinutes(ctx, &sessionv1.UpdateMinutesRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid, Text: text})
		return r.GetResult(), err
	})
}

// ShowArtifact shows an artifact on the stage.
func (c *Client) ShowArtifact(ctx context.Context, sessionID, artifactID string) (*sessionv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessionv1.CommandResult, error) {
		r, err := c.Session.ShowArtifact(ctx, &sessionv1.ShowArtifactRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid, ArtifactId: artifactID})
		return r.GetResult(), err
	})
}

// EndSession ends the session for everyone.
func (c *Client) EndSession(ctx context.Context, sessionID string) (*sessionv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessionv1.CommandResult, error) {
		r, err := c.Session.EndSession(ctx, &sessionv1.EndSessionRequest{SessionId: sessionID, CommandId: id, ParticipantId: pid})
		return r.GetResult(), err
	})
}

// StageLink creates a read-only link to the stage of a session.
func (c *Client) StageLink(ctx context.Context, sessionID string) (*identityv1.CreateStageLinkResponse, error) {
	cmdID := ids.NewCommandID()
	var res *identityv1.CreateStageLinkResponse
	err := Retry(ctx, func() (err error) {
		res, err = c.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{CommandId: cmdID, SessionId: sessionID})
		return err
	})
	return res, err
}

// RegisterKey registers a public key for a participant. Only admin keys may call it.
func (c *Client) RegisterKey(ctx context.Context, req *identityv1.RegisterKeyRequest) (*identityv1.RegisterKeyResponse, error) {
	req.CommandId = ids.NewCommandID()
	var res *identityv1.RegisterKeyResponse
	err := Retry(ctx, func() (err error) {
		res, err = c.Identity.RegisterKey(ctx, req)
		return err
	})
	return res, err
}

// RevokeKey revokes a key by its key ID. Only admin keys may call it.
func (c *Client) RevokeKey(ctx context.Context, keyID string) error {
	cmdID := ids.NewCommandID()
	return Retry(ctx, func() error {
		_, err := c.Identity.RevokeKey(ctx, &identityv1.RevokeKeyRequest{CommandId: cmdID, KeyId: keyID})
		return err
	})
}

// RenderDeck renders a slide deck. The author is the caller.
func (c *Client) RenderDeck(ctx context.Context, req *slidesv1.RenderDeckRequest) (*slidesv1.RenderDeckResponse, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req.CommandId, req.AuthorParticipantId = ids.NewCommandID(), pid
	var res *slidesv1.RenderDeckResponse
	err = Retry(ctx, func() (err error) {
		res, err = c.Slides.RenderDeck(ctx, req)
		return err
	})
	return res, err
}

// GetDeck loads a slide deck with its input and its rendered output by its ID, for example
// "slides/42".
func (c *Client) GetDeck(ctx context.Context, id string) (*slidesv1.Deck, error) {
	var res *slidesv1.GetDeckResponse
	err := Retry(ctx, func() (err error) {
		res, err = c.Slides.GetDeck(ctx, &slidesv1.GetDeckRequest{Id: id})
		return err
	})
	return res.GetDeck(), err
}

// RecordDecision records a decision. The author is the caller. sessionID may be empty.
func (c *Client) RecordDecision(ctx context.Context, text, sessionID string) (*decisionv1.RecordDecisionResponse, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req := &decisionv1.RecordDecisionRequest{CommandId: ids.NewCommandID(), Text: text, AuthorParticipantId: pid, SessionId: sessionID}
	var res *decisionv1.RecordDecisionResponse
	err = Retry(ctx, func() (err error) {
		res, err = c.Decision.RecordDecision(ctx, req)
		return err
	})
	return res, err
}

// ListDecisions lists decisions after a cursor. sessionID may be empty (all); limit 0 is the
// default of the hub.
func (c *Client) ListDecisions(ctx context.Context, after uint32, sessionID string, limit uint32) ([]*decisionv1.Decision, error) {
	var res *decisionv1.ListDecisionsResponse
	err := Retry(ctx, func() (err error) {
		res, err = c.Decision.ListDecisions(ctx, &decisionv1.ListDecisionsRequest{AfterId: after, SessionId: sessionID, Limit: limit})
		return err
	})
	return res.GetDecisions(), err
}

// AddNote adds a note. The author is the caller. sessionID may be empty.
func (c *Client) AddNote(ctx context.Context, text, sessionID string) (*notev1.AddNoteResponse, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req := &notev1.AddNoteRequest{CommandId: ids.NewCommandID(), Text: text, AuthorParticipantId: pid, SessionId: sessionID}
	var res *notev1.AddNoteResponse
	err = Retry(ctx, func() (err error) {
		res, err = c.Note.AddNote(ctx, req)
		return err
	})
	return res, err
}

// ListNotes lists notes after a cursor. sessionID may be empty (all); limit 0 is the default of
// the hub.
func (c *Client) ListNotes(ctx context.Context, after uint32, sessionID string, limit uint32) ([]*notev1.Note, error) {
	var res *notev1.ListNotesResponse
	err := Retry(ctx, func() (err error) {
		res, err = c.Note.ListNotes(ctx, &notev1.ListNotesRequest{AfterId: after, SessionId: sessionID, Limit: limit})
		return err
	})
	return res.GetNotes(), err
}

// GetMinutes returns the stored minutes of an ended session.
func (c *Client) GetMinutes(ctx context.Context, sessionID string) (*minutesv1.Minutes, error) {
	var res *minutesv1.GetMinutesResponse
	err := Retry(ctx, func() (err error) {
		res, err = c.Minutes.GetMinutes(ctx, &minutesv1.GetMinutesRequest{SessionId: sessionID})
		return err
	})
	return res.GetMinutes(), err
}

// ListMinutes lists stored minutes after a cursor. sessionID may be empty (all); limit 0 is the
// default of the hub.
func (c *Client) ListMinutes(ctx context.Context, after uint32, sessionID string, limit uint32) ([]*minutesv1.Minutes, error) {
	var res *minutesv1.ListMinutesResponse
	err := Retry(ctx, func() (err error) {
		res, err = c.Minutes.ListMinutes(ctx, &minutesv1.ListMinutesRequest{AfterId: after, SessionId: sessionID, Limit: limit})
		return err
	})
	return res.GetMinutes(), err
}

// Events returns the events of a session after a cursor, at most limit of them (0: the server's
// default), and whether there are more (dec-events-from-cursor). Event IDs have no gaps, so a
// participant that finds a gap in its copy reads the missing events here.
func (c *Client) Events(ctx context.Context, sessionID string, after, limit uint32) ([]*sessionv1.Event, bool, error) {
	var res *sessionv1.GetEventsResponse
	err := Retry(ctx, func() (err error) {
		res, err = c.Session.GetEvents(ctx, &sessionv1.GetEventsRequest{SessionId: sessionID, AfterEventId: after, Limit: limit})
		return err
	})
	return res.GetEvents(), res.GetMore(), err
}

// endedLog reports whether the local log at path ends with session.ended.
func endedLog(path string) bool {
	evs, err := ReadLog(path)
	return err == nil && len(evs) > 0 && evs[len(evs)-1].GetType() == events.SessionEnded
}
