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
	artifactv1 "github.com/mrsimpson/agentic-office/server/artifacts/api"
	identityv1 "github.com/mrsimpson/agentic-office/server/identity/api"
	journalv1 "github.com/mrsimpson/agentic-office/server/journal/api"
	sessionv1 "github.com/mrsimpson/agentic-office/server/session/api"
	"github.com/mrsimpson/agentic-office/server/session/api/events"
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

// Retract retracts an own utterance by its event ID.
func (c *Client) Retract(ctx context.Context, sessionID string, utteranceEventID uint32) (*sessionv1.CommandResult, error) {
	return c.command(ctx, func(pid, id string) (*sessionv1.CommandResult, error) {
		r, err := c.Session.RetractUtterance(ctx, &sessionv1.RetractUtteranceRequest{SessionId: sessionID, CommandId: id,
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

// RenderSlides renders a deck. The author is the caller.
func (c *Client) RenderSlides(ctx context.Context, req *artifactv1.RenderSlidesRequest) (*artifactv1.RenderSlidesResponse, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req.CommandId, req.AuthorParticipantId = ids.NewCommandID(), pid
	var res *artifactv1.RenderSlidesResponse
	err = Retry(ctx, func() (err error) {
		res, err = c.Artifact.RenderSlides(ctx, req)
		return err
	})
	return res, err
}

// GetArtifact loads an artifact by its ID, for example "slides/42".
func (c *Client) GetArtifact(ctx context.Context, id string) (*artifactv1.Artifact, error) {
	var res *artifactv1.GetArtifactResponse
	err := Retry(ctx, func() (err error) {
		res, err = c.Artifact.GetArtifact(ctx, &artifactv1.GetArtifactRequest{Id: id})
		return err
	})
	return res.GetArtifact(), err
}

// AppendJournal writes a journal entry. The author is the caller. sessionID may be empty.
func (c *Client) AppendJournal(ctx context.Context, kind journalv1.EntryKind, text, sessionID string) (*journalv1.AppendEntryResponse, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req := &journalv1.AppendEntryRequest{CommandId: ids.NewCommandID(), Kind: kind, Text: text, AuthorParticipantId: pid, SessionId: sessionID}
	var res *journalv1.AppendEntryResponse
	err = Retry(ctx, func() (err error) {
		res, err = c.Journal.AppendEntry(ctx, req)
		return err
	})
	return res, err
}

// ListJournal lists journal entries.
func (c *Client) ListJournal(ctx context.Context, req *journalv1.ListEntriesRequest) ([]*journalv1.JournalEntry, error) {
	var res *journalv1.ListEntriesResponse
	err := Retry(ctx, func() (err error) {
		res, err = c.Journal.ListEntries(ctx, req)
		return err
	})
	return res.GetEntries(), err
}

// endedLog reports whether the local log at path ends with session.ended.
func endedLog(path string) bool {
	evs, err := ReadLog(path)
	return err == nil && len(evs) > 0 && evs[len(evs)-1].GetType() == events.SessionEnded
}
