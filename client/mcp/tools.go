package mcp

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/mrsimpson/agentic-office/client/mcp/tools"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	"github.com/mrsimpson/agentic-office/client/replicator/control"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
)

// Tool names use "_" where the architecture writes "." (meeting.join is meeting_join): many MCP
// clients and model APIs accept only letters, digits, "_" and "-" in tool names.

// tool adds a tool whose handler gets a client. The handler returns the output object, or an
// error that becomes {"error":{"code","message"}} with IsError set.
func tool[In any](srv *sdk.Server, s *Server, t *sdk.Tool, f func(ctx context.Context, c coreapi.Client, in In) (any, error)) {
	sdk.AddTool(srv, t, func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
		c, err := s.dial()
		if err == nil {
			var v any
			if v, err = f(ctx, c, in); err == nil {
				return okResult(v), nil, nil
			}
		}
		return errorResult(errorCode(err), err.Error()), nil, nil
	})
}

// sessionTool adds a session command: it resolves the session and returns {"event", "duplicate"}.
func sessionTool[In any](srv *sdk.Server, s *Server, t *sdk.Tool, session func(In) string,
	f func(ctx context.Context, c coreapi.Client, sid string, in In) (*sessioncommandsv1.CommandResult, error)) {
	tool(srv, s, t, func(ctx context.Context, c coreapi.Client, in In) (any, error) {
		sid, err := s.sessionID(ctx, c, session(in))
		if err != nil {
			return nil, err
		}
		res, err := f(ctx, c, sid, in)
		if err != nil {
			return nil, err
		}
		return coreapi.ResultJSON(res), nil
	})
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

func (s *Server) addTools(srv *sdk.Server) {
	tool(srv, s, tools.MeetingJoin,
		func(ctx context.Context, c coreapi.Client, in tools.JoinIn) (any, error) {
			if blank(in.Session) {
				return nil, usagef("session is required")
			}
			j, err := c.Join(ctx, in.Session, coreapi.JoinOptions{DisplayName: in.Name})
			if err != nil {
				return nil, err
			}
			pid, err := s.startReplicator(c, j)
			if err != nil {
				return nil, err
			}
			return map[string]any{"sessionId": j.SessionID, "participantId": j.ParticipantID, "logPath": j.Paths.Log,
				"replicatorPid": pid, "event": coreapi.EventJSON(j.Result.GetEvent()), "duplicate": j.Result.GetDuplicate()}, nil
		})

	tool(srv, s, tools.MeetingLog,
		func(ctx context.Context, c coreapi.Client, in tools.SessionIn) (any, error) {
			sid, err := s.sessionID(ctx, c, in.Session)
			if err != nil {
				return nil, err
			}
			pid, err := c.ParticipantID(ctx)
			if err != nil {
				return nil, err
			}
			p, err := c.Config().Paths(sid, pid)
			if err != nil {
				return nil, err
			}
			return map[string]any{"sessionId": sid, "participantId": pid, "logPath": p.Log,
				"replicatorRunning": control.Running(p.Log)}, nil
		})

	sessionTool(srv, s, tools.MeetingLeave,
		func(in tools.SessionIn) string { return in.Session },
		func(ctx context.Context, c coreapi.Client, sid string, _ tools.SessionIn) (*sessioncommandsv1.CommandResult, error) {
			res, err := c.Leave(ctx, sid)
			if err != nil {
				return nil, err
			}
			// The replicator stops after it wrote participant.left. Wait for it, so that a join
			// right after this starts a new one.
			if pid, err := c.ParticipantID(ctx); err == nil {
				if p, err := c.Config().Paths(sid, pid); err == nil {
					deadline := time.Now().Add(5 * time.Second)
					for control.Running(p.Log) && time.Now().Before(deadline) && ctx.Err() == nil {
						time.Sleep(20 * time.Millisecond)
					}
				}
			}
			return res, nil
		})

	simple := func(t *sdk.Tool, f func(coreapi.Client, context.Context, string) (*sessioncommandsv1.CommandResult, error)) {
		sessionTool(srv, s, t,
			func(in tools.SessionIn) string { return in.Session },
			func(ctx context.Context, c coreapi.Client, sid string, _ tools.SessionIn) (*sessioncommandsv1.CommandResult, error) {
				return f(c, ctx, sid)
			})
	}
	simple(tools.MeetingPause, coreapi.Client.Pause)
	simple(tools.MeetingResume, coreapi.Client.Resume)
	simple(tools.MeetingEnd, coreapi.Client.EndSession)

	sessionTool(srv, s, tools.UtteranceSay,
		func(in tools.SayIn) string { return in.Session },
		func(ctx context.Context, c coreapi.Client, sid string, in tools.SayIn) (*sessioncommandsv1.CommandResult, error) {
			if blank(in.Text) {
				return nil, usagef("text is required")
			}
			return c.Say(ctx, sid, in.Text)
		})

	sessionTool(srv, s, tools.UtteranceOffTheRecord,
		func(in tools.OffTheRecordIn) string { return in.Session },
		func(ctx context.Context, c coreapi.Client, sid string, in tools.OffTheRecordIn) (*sessioncommandsv1.CommandResult, error) {
			switch {
			case in.Last && in.EventID != 0, !in.Last && in.EventID == 0:
				return nil, usagef("give eventId or last=true, not both and not neither")
			case in.Last:
				return c.MarkOffTheRecordLast(ctx, sid)
			}
			return c.MarkOffTheRecord(ctx, sid, in.EventID)
		})

	sessionTool(srv, s, tools.SignalTurnEnd,
		func(in tools.TurnEndIn) string { return in.Session },
		func(ctx context.Context, c coreapi.Client, sid string, in tools.TurnEndIn) (*sessioncommandsv1.CommandResult, error) {
			return c.TurnEnd(ctx, sid, in.Participant)
		})

	sessionTool(srv, s, tools.SignalSilenceLong,
		func(in tools.SilenceIn) string { return in.Session },
		func(ctx context.Context, c coreapi.Client, sid string, in tools.SilenceIn) (*sessioncommandsv1.CommandResult, error) {
			d, err := time.ParseDuration(in.Duration)
			if err != nil || d <= 0 {
				return nil, usagef("duration must be a positive duration such as \"12s\", not %q", in.Duration)
			}
			return c.SilenceLong(ctx, sid, d)
		})

	sessionTool(srv, s, tools.SignalAddressed,
		func(in tools.AddressedIn) string { return in.Session },
		func(ctx context.Context, c coreapi.Client, sid string, in tools.AddressedIn) (*sessioncommandsv1.CommandResult, error) {
			if blank(in.Participant) || in.UtteranceEventID == 0 {
				return nil, usagef("participant and utteranceEventId are required")
			}
			return c.Addressed(ctx, sid, in.Participant, in.UtteranceEventID)
		})

	sessionTool(srv, s, tools.HandRaise,
		func(in tools.HandIn) string { return in.Session },
		func(ctx context.Context, c coreapi.Client, sid string, in tools.HandIn) (*sessioncommandsv1.CommandResult, error) {
			return c.RaiseHand(ctx, sid, in.Topic)
		})

	sessionTool(srv, s, tools.MinutesUpdate,
		func(in tools.MinutesIn) string { return in.Session },
		func(ctx context.Context, c coreapi.Client, sid string, in tools.MinutesIn) (*sessioncommandsv1.CommandResult, error) {
			text := in.Text
			switch {
			case in.File != "" && in.Text != "":
				return nil, usagef("give text or file, not both")
			case in.File != "":
				b, err := os.ReadFile(in.File)
				if err != nil {
					return nil, err
				}
				text = string(b)
			}
			if blank(text) {
				return nil, usagef("text or file is required")
			}
			return c.UpdateMinutes(ctx, sid, text)
		})

	sessionTool(srv, s, tools.StageShow,
		func(in tools.StageShowIn) string { return in.Session },
		func(ctx context.Context, c coreapi.Client, sid string, in tools.StageShowIn) (*sessioncommandsv1.CommandResult, error) {
			if blank(in.ArtifactID) {
				return nil, usagef("artifactId is required")
			}
			return c.ShowArtifact(ctx, sid, in.ArtifactID)
		})

	tool(srv, s, tools.StageLink,
		func(ctx context.Context, c coreapi.Client, in tools.SessionIn) (any, error) {
			sid, err := s.sessionID(ctx, c, in.Session)
			if err != nil {
				return nil, err
			}
			res, err := c.StageLink(ctx, sid)
			if err != nil {
				return nil, err
			}
			return map[string]any{"url": res.GetUrl(), "expiresAt": res.GetExpiresAt().AsTime().Format(time.RFC3339)}, nil
		})

	tool(srv, s, tools.SlidesRender,
		func(ctx context.Context, c coreapi.Client, in tools.SlidesIn) (any, error) {
			n := 0
			for _, set := range []bool{len(in.Slides) > 0, in.Markdown != "", in.File != ""} {
				if set {
					n++
				}
			}
			if n != 1 {
				return nil, usagef("give exactly one of slides, markdown or file")
			}
			var req *slidesv1.RenderDeckRequest
			var err error
			switch {
			case in.File != "":
				var b []byte
				if b, err = os.ReadFile(in.File); err != nil {
					return nil, err
				}
				req, err = coreapi.ParseDeck(in.File, b)
			case in.Markdown != "":
				req, err = coreapi.ParseDeckMarkdown([]byte(in.Markdown))
			default:
				req = &slidesv1.RenderDeckRequest{Title: in.Title}
				for _, sl := range in.Slides {
					req.Slides = append(req.Slides, &slidesv1.Slide{Title: sl.Title, Bullets: sl.Bullets, Notes: sl.Notes})
				}
			}
			if err != nil {
				return nil, err
			}
			if len(req.GetSlides()) == 0 {
				return nil, usagef("the deck has no slides")
			}
			res, err := c.RenderDeck(ctx, req)
			if err != nil {
				return nil, err
			}
			return map[string]any{"id": res.GetDeck().GetId(), "mediaType": res.GetDeck().GetMediaType(),
				"duplicate": res.GetDuplicate()}, nil
		})

	tool(srv, s, tools.SlidesGet,
		func(ctx context.Context, c coreapi.Client, in tools.DeckIn) (any, error) {
			if blank(in.ID) {
				return nil, usagef("id is required")
			}
			a, err := c.GetDeck(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if in.Out != "" {
				if err := os.WriteFile(in.Out, a.GetContent(), 0o600); err != nil {
					return nil, err
				}
				return map[string]any{"id": a.GetId(), "mediaType": a.GetMediaType(), "file": in.Out}, nil
			}
			return map[string]any{"deck": coreapi.MessageJSON(a)}, nil
		})

	tool(srv, s, tools.DecisionRecord,
		func(ctx context.Context, c coreapi.Client, in tools.RecordIn) (any, error) {
			if blank(in.Text) {
				return nil, usagef("text is required")
			}
			res, err := c.RecordDecision(ctx, in.Text, in.Session)
			if err != nil {
				return nil, err
			}
			return map[string]any{"decision": coreapi.MessageJSON(res.GetDecision()), "duplicate": res.GetDuplicate()}, nil
		})

	tool(srv, s, tools.DecisionList,
		func(ctx context.Context, c coreapi.Client, in tools.ListIn) (any, error) {
			ds, err := c.ListDecisions(ctx, in.AfterID, in.Session, in.Limit)
			if err != nil {
				return nil, err
			}
			out := make([]any, len(ds))
			for i, d := range ds {
				out[i] = coreapi.MessageJSON(d)
			}
			return map[string]any{"decisions": out}, nil
		})

	tool(srv, s, tools.NoteAdd,
		func(ctx context.Context, c coreapi.Client, in tools.RecordIn) (any, error) {
			if blank(in.Text) {
				return nil, usagef("text is required")
			}
			res, err := c.AddNote(ctx, in.Text, in.Session)
			if err != nil {
				return nil, err
			}
			return map[string]any{"note": coreapi.MessageJSON(res.GetNote()), "duplicate": res.GetDuplicate()}, nil
		})

	tool(srv, s, tools.NoteList,
		func(ctx context.Context, c coreapi.Client, in tools.ListIn) (any, error) {
			ns, err := c.ListNotes(ctx, in.AfterID, in.Session, in.Limit)
			if err != nil {
				return nil, err
			}
			out := make([]any, len(ns))
			for i, n := range ns {
				out[i] = coreapi.MessageJSON(n)
			}
			return map[string]any{"notes": out}, nil
		})

	tool(srv, s, tools.MinutesGet,
		func(ctx context.Context, c coreapi.Client, in tools.MinutesGetIn) (any, error) {
			sid, err := s.sessionID(ctx, c, in.Session)
			if err != nil {
				return nil, err
			}
			m, err := c.GetMinutes(ctx, sid)
			if err != nil {
				return nil, err
			}
			return map[string]any{"minutes": coreapi.MessageJSON(m)}, nil
		})

	tool(srv, s, tools.MinutesList,
		func(ctx context.Context, c coreapi.Client, in tools.ListIn) (any, error) {
			ms, err := c.ListMinutes(ctx, in.AfterID, in.Session, in.Limit)
			if err != nil {
				return nil, err
			}
			out := make([]any, len(ms))
			for i, m := range ms {
				out[i] = coreapi.MessageJSON(m)
			}
			return map[string]any{"minutes": out}, nil
		})

	tool(srv, s, tools.TaskCreate,
		func(ctx context.Context, c coreapi.Client, in tools.TaskCreateIn) (any, error) {
			if blank(in.Title) {
				return nil, usagef("title is required")
			}
			res, err := c.CreateTask(ctx, coreapi.TaskDraft{Title: in.Title, Description: in.Description, Assignee: in.Assignee,
				Status: in.Status, SessionID: in.Session})
			if err != nil {
				return nil, err
			}
			return map[string]any{"task": coreapi.MessageJSON(res.GetTask()), "duplicate": res.GetDuplicate()}, nil
		})

	tool(srv, s, tools.TaskGet,
		func(ctx context.Context, c coreapi.Client, in tools.TaskRefIn) (any, error) {
			if blank(in.Ref) {
				return nil, usagef("ref is required")
			}
			t, err := c.GetTask(ctx, in.Ref)
			if err != nil {
				return nil, err
			}
			return map[string]any{"task": coreapi.MessageJSON(t)}, nil
		})

	tool(srv, s, tools.TaskList,
		func(ctx context.Context, c coreapi.Client, in tools.TaskListIn) (any, error) {
			ts, next, err := c.ListTasks(ctx, coreapi.TaskFilter{Statuses: in.Statuses, Assignee: in.Assignee, SessionID: in.Session,
				PageToken: in.PageToken, Limit: in.Limit})
			if err != nil {
				return nil, err
			}
			out := make([]any, len(ts))
			for i, t := range ts {
				out[i] = coreapi.MessageJSON(t)
			}
			return map[string]any{"tasks": out, "nextPageToken": next}, nil
		})

	tool(srv, s, tools.TaskUpdate,
		func(ctx context.Context, c coreapi.Client, in tools.TaskUpdateIn) (any, error) {
			if blank(in.Ref) {
				return nil, usagef("ref is required")
			}
			res, err := c.UpdateTask(ctx, in.Ref, coreapi.TaskChange{Title: in.Title, Description: in.Description,
				Assignee: in.Assignee, Status: in.Status, SessionID: in.Session})
			if err != nil {
				return nil, err
			}
			return map[string]any{"task": coreapi.MessageJSON(res.GetTask()), "duplicate": res.GetDuplicate()}, nil
		})

	tool(srv, s, tools.SessionImport,
		func(ctx context.Context, c coreapi.Client, in tools.ImportIn) (any, error) {
			conv := in.Conversation
			switch {
			case (in.File == "") == (conv == nil):
				return nil, usagef("give exactly one of file or conversation")
			case in.File != "":
				f, err := os.Open(in.File)
				if err != nil {
					return nil, err
				}
				defer f.Close()
				if conv, err = coreapi.ParseConversation(f); err != nil {
					return nil, err
				}
			}
			im, err := c.Import(ctx, in.Session, conv)
			if err != nil {
				return nil, err
			}
			return coreapi.ImportedJSON(im), nil
		})

	tool(srv, s, tools.MeetingEvents,
		func(ctx context.Context, c coreapi.Client, in tools.EventsIn) (any, error) {
			sid, err := s.sessionID(ctx, c, in.Session)
			if err != nil {
				return nil, err
			}
			evs, more, err := c.Events(ctx, sid, in.After, in.Limit)
			if err != nil {
				return nil, err
			}
			out := make([]any, len(evs))
			for i, ev := range evs {
				out[i] = coreapi.EventJSON(ev)
			}
			return map[string]any{"events": out, "more": more}, nil
		})

	tool(srv, s, tools.Whoami,
		func(ctx context.Context, c coreapi.Client, _ tools.NoIn) (any, error) {
			pid, err := c.ParticipantID(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"participantId": pid, "keyId": c.KeyID()}, nil
		})
}
