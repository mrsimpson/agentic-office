package mcp

import (
	"context"
	"os"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	artifactv1 "github.com/mrsimpson/agentic-office/hub/artifacts/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	journalv1 "github.com/mrsimpson/agentic-office/hub/journal/api"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"

	"github.com/mrsimpson/agentic-office/client/core"
	"github.com/mrsimpson/agentic-office/client/replicator"
)

// Tool names use "_" where the architecture writes "." (meeting.join is meeting_join): many MCP
// clients and model APIs accept only letters, digits, "_" and "-" in tool names.

// instructions are sent to the MCP client when it connects. Many clients show them to the model.
const instructions = `You take part in a meeting of the agentic office as a participant. You read what happens from a local file and act through these tools.

1. Call meeting_join with the session ID. It returns "logPath", the local session log. While this MCP server runs, it copies every event of the session into that file. The copy stops when this server stops, when you leave, or when the session ends.
2. Follow the file in the background with a filter, and react only to what matters to you. Do not wait for the tools to tell you about new events; they do not.
3. Act with the tools. Each tool answers at once. A session command returns {"event": …, "duplicate": …}: the event it created, in the same form as its line in the log. So you can find your own actions in the log.

` + logHelp

// logHelp explains the local log. It is part of the instructions and of the description of
// meeting_join, because not every client shows the instructions to the model.
const logHelp = `How to read the log file:
- One JSON object per line, append-only. "id" is a number that grows by 1 per event; "type" is the dotted event type; "actorParticipantId" is who caused it; the payload is in a field named after the type in camelCase, for example {"id":7,"type":"utterance.final","actorParticipantId":"ada","utteranceFinal":{"participantId":"ada","text":"…"}}.
- Follow it in the background, for example:
  tail -n +1 -f "$LOG" | grep --line-buffered '"type":"turn.end"\|"type":"addressed"'
  or read it again from time to time and keep the last id you handled:
  jq -c 'select(.id > 41 and (.type == "utterance.final" or .type == "addressed"))' "$LOG"
- Event types: utterance.final (someone said or typed a sentence), utterance.off_the_record (the speaker marked a sentence off the record: never use or repeat that sentence, not in minutes and not in the journal; the original line stays in your file), turn.end (a speaker finished; a good moment to answer), addressed (someone named a participant; check addressed.participantId), silence.long, hand.raised, minutes.updated, artifact.shown, participant.joined, participant.left, participant.paused, participant.resumed, session.ended (the last line; the file is complete).
- Usually filter for turn.end and addressed, and read the utterance.final lines before them for the context.
- IDs have no gaps. If you see a gap (43 after 41), read the missing events with meeting_events (after 41). meeting_events also works without the file, if you poll it with your last id.`

// tool adds a tool whose handler gets a client. The handler returns the output object, or an
// error that becomes {"error":{"code","message"}} with IsError set.
func tool[In any](srv *sdk.Server, s *Server, t *sdk.Tool, f func(ctx context.Context, c *core.Client, in In) (any, error)) {
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
	f func(ctx context.Context, c *core.Client, sid string, in In) (*sessionv1.CommandResult, error)) {
	tool(srv, s, t, func(ctx context.Context, c *core.Client, in In) (any, error) {
		sid, err := s.sessionID(ctx, c, session(in))
		if err != nil {
			return nil, err
		}
		res, err := f(ctx, c, sid, in)
		if err != nil {
			return nil, err
		}
		return core.ResultJSON(res), nil
	})
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

func ptr[T any](v T) *T { return &v }

// Annotations: hints for clients. Session commands change the meeting; reads do not.
var (
	readOnly = &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}
	additive = &sdk.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)}
)

type joinIn struct {
	Session string `json:"session" jsonschema:"Session ID to join, for example \"weekly\"."`
	Name    string `json:"name,omitempty" jsonschema:"Display name in this session. Default: the registered name."`
}

type sessionIn struct {
	Session string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

type sayIn struct {
	Text    string `json:"text" jsonschema:"What you say: one or a few sentences."`
	Session string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

type offTheRecordIn struct {
	EventID uint32 `json:"eventId,omitempty" jsonschema:"Event ID (the id of its utterance.final line) of your own sentence to mark off the record."`
	Last    bool   `json:"last,omitempty" jsonschema:"Mark off the record your newest sentence that is not off the record yet, read from the local log. Use either eventId or last."`
	Session string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

type turnEndIn struct {
	Participant string `json:"participant,omitempty" jsonschema:"Participant whose turn ended. Default: you."`
	Session     string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

type silenceIn struct {
	Duration string `json:"duration" jsonschema:"Length of the silence, for example \"12s\" or \"1m30s\"."`
	Session  string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

type addressedIn struct {
	Participant      string `json:"participant" jsonschema:"Participant whose name was mentioned."`
	UtteranceEventID uint32 `json:"utteranceEventId" jsonschema:"Event ID of the utterance.final that mentioned the name."`
	Session          string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

type handIn struct {
	Topic   string `json:"topic,omitempty" jsonschema:"What you want to say, in a few words."`
	Session string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

type minutesIn struct {
	Text    string `json:"text,omitempty" jsonschema:"The full new minutes (Markdown). They replace the old minutes. Use text or file."`
	File    string `json:"file,omitempty" jsonschema:"Read the minutes from this local file instead."`
	Session string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

type stageShowIn struct {
	ArtifactID string `json:"artifactId" jsonschema:"Artifact to show, for example \"slides/42\" from slides_render."`
	Session    string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

type slideIn struct {
	Title   string   `json:"title" jsonschema:"Title of the slide."`
	Bullets []string `json:"bullets,omitempty" jsonschema:"Bullet points."`
	Notes   string   `json:"notes,omitempty" jsonschema:"Speaker notes."`
}

type slidesIn struct {
	Title    string    `json:"title,omitempty" jsonschema:"Title of the deck (with slides)."`
	Slides   []slideIn `json:"slides,omitempty" jsonschema:"The slides. Use slides, markdown or file."`
	Markdown string    `json:"markdown,omitempty" jsonschema:"The deck in Markdown: \"# \" deck title, \"## \" starts a slide, \"- \" bullet, other text is the notes."`
	File     string    `json:"file,omitempty" jsonschema:"A local deck file: .md (Markdown as above) or .json ({\"title\", \"slides\"})."`
}

type artifactIn struct {
	ID  string `json:"id" jsonschema:"Artifact ID, for example \"slides/42\"."`
	Out string `json:"out,omitempty" jsonschema:"Write the content to this local file instead of returning it (base64) in the result."`
}

type journalAppendIn struct {
	Kind    string `json:"kind" jsonschema:"decision, task, minutes or note."`
	Text    string `json:"text" jsonschema:"The entry."`
	Session string `json:"session,omitempty" jsonschema:"Link the entry to this session. Default: no session (unlike the session commands)."`
}

type journalListIn struct {
	Kind         string `json:"kind,omitempty" jsonschema:"Only entries of this kind: decision, task, minutes or note."`
	AfterEntryID uint32 `json:"afterEntryId,omitempty" jsonschema:"Only entries with an ID greater than this."`
	Limit        uint32 `json:"limit,omitempty" jsonschema:"At most this many entries. 0: the server's default."`
	Session      string `json:"session,omitempty" jsonschema:"Only entries of this session. Default: all."`
}

type eventsIn struct {
	Session string `json:"session,omitempty" jsonschema:"The session. Default: the only session you joined from this machine."`
	After   uint32 `json:"after,omitempty" jsonschema:"The cursor: only events with an ID greater than this. 0 reads from the start."`
	Limit   uint32 `json:"limit,omitempty" jsonschema:"At most this many events. 0: the server's default (100); at most 1000."`
}

type noIn struct{}

func (s *Server) addTools(srv *sdk.Server) {
	tool(srv, s, &sdk.Tool{Name: "meeting_join", Title: "Join a meeting", Annotations: additive,
		Description: `Join a meeting session and start copying its events into a local log file. Returns {"sessionId", "participantId", "logPath", "replicatorPid", "event", "duplicate"}. Joining again is safe: you stay the same participant and get the same file. The copy runs as long as this MCP server runs (replicatorPid is the process that writes the file), until you leave or the session ends; call meeting_join again to restart it.

` + logHelp},
		func(ctx context.Context, c *core.Client, in joinIn) (any, error) {
			if blank(in.Session) {
				return nil, usagef("session is required")
			}
			j, err := c.Join(ctx, in.Session, core.JoinOptions{DisplayName: in.Name})
			if err != nil {
				return nil, err
			}
			pid, err := s.startReplicator(c, j)
			if err != nil {
				return nil, err
			}
			return map[string]any{"sessionId": j.SessionID, "participantId": j.ParticipantID, "logPath": j.Paths.Log,
				"replicatorPid": pid, "event": core.EventJSON(j.Result.GetEvent()), "duplicate": j.Result.GetDuplicate()}, nil
		})

	tool(srv, s, &sdk.Tool{Name: "meeting_log", Title: "Path of the local log", Annotations: readOnly,
		Description: `Return the local log file of a session you joined, and whether a replicator writes it now: {"sessionId", "participantId", "logPath", "replicatorRunning"}. If replicatorRunning is false and the session has not ended, call meeting_join again.`},
		func(ctx context.Context, c *core.Client, in sessionIn) (any, error) {
			sid, err := s.sessionID(ctx, c, in.Session)
			if err != nil {
				return nil, err
			}
			pid, err := c.ParticipantID(ctx)
			if err != nil {
				return nil, err
			}
			p, err := c.Paths(sid, pid)
			if err != nil {
				return nil, err
			}
			return map[string]any{"sessionId": sid, "participantId": pid, "logPath": p.Log,
				"replicatorRunning": replicator.Running(p.Log)}, nil
		})

	sessionTool(srv, s, &sdk.Tool{Name: "meeting_leave", Title: "Leave the meeting", Annotations: additive,
		Description: `Leave the session. The log file gets your participant.left line and then stops growing. Returns {"event", "duplicate"}.`},
		func(in sessionIn) string { return in.Session },
		func(ctx context.Context, c *core.Client, sid string, _ sessionIn) (*sessionv1.CommandResult, error) {
			res, err := c.Leave(ctx, sid)
			if err != nil {
				return nil, err
			}
			// The replicator stops after it wrote participant.left. Wait for it, so that a join
			// right after this starts a new one.
			if pid, err := c.ParticipantID(ctx); err == nil {
				if p, err := c.Paths(sid, pid); err == nil {
					deadline := time.Now().Add(5 * time.Second)
					for replicator.Running(p.Log) && time.Now().Before(deadline) && ctx.Err() == nil {
						time.Sleep(20 * time.Millisecond)
					}
				}
			}
			return res, nil
		})

	simple := func(name, title, desc string, f func(*core.Client, context.Context, string) (*sessionv1.CommandResult, error)) {
		sessionTool(srv, s, &sdk.Tool{Name: name, Title: title, Annotations: additive, Description: desc},
			func(in sessionIn) string { return in.Session },
			func(ctx context.Context, c *core.Client, sid string, _ sessionIn) (*sessionv1.CommandResult, error) {
				return f(c, ctx, sid)
			})
	}
	simple("meeting_pause", "Pause", `Pause your capture (participant.paused). For humans whose speech is transcribed; an agent rarely needs it. Returns {"event", "duplicate"}.`,
		(*core.Client).Pause)
	simple("meeting_resume", "Resume", `End a pause (participant.resumed). Returns {"event", "duplicate"}.`, (*core.Client).Resume)
	simple("meeting_end", "End the meeting for everyone",
		`End the session for all participants. session.ended becomes the last line of every log. Only do this when asked to. Returns {"event", "duplicate"}.`,
		(*core.Client).EndSession)

	sessionTool(srv, s, &sdk.Tool{Name: "utterance_say", Title: "Say something", Annotations: additive,
		Description: `Say something in the meeting (a typed utterance, utterance.final with modality text). The stage shows it, and every participant reads it in their log. Returns {"event", "duplicate"}; event.id is the ID if you want to mark it off the record later.`},
		func(in sayIn) string { return in.Session },
		func(ctx context.Context, c *core.Client, sid string, in sayIn) (*sessionv1.CommandResult, error) {
			if blank(in.Text) {
				return nil, usagef("text is required")
			}
			return c.Say(ctx, sid, in.Text)
		})

	sessionTool(srv, s, &sdk.Tool{Name: "utterance_off_the_record", Title: "Mark a sentence off the record", Annotations: additive,
		Description: `Mark one of your own sentences off the record. What is said stays said: this adds an utterance.off_the_record line that points to the sentence. Readers who read the session later get only a placeholder without its text, and nobody may use the sentence in minutes or the journal. You cannot undo it. Give eventId, or last=true for your newest sentence. Returns {"event", "duplicate"}.`},
		func(in offTheRecordIn) string { return in.Session },
		func(ctx context.Context, c *core.Client, sid string, in offTheRecordIn) (*sessionv1.CommandResult, error) {
			switch {
			case in.Last && in.EventID != 0, !in.Last && in.EventID == 0:
				return nil, usagef("give eventId or last=true, not both and not neither")
			case in.Last:
				return c.MarkOffTheRecordLast(ctx, sid)
			}
			return c.MarkOffTheRecord(ctx, sid, in.EventID)
		})

	sessionTool(srv, s, &sdk.Tool{Name: "signal_turn_end", Title: "Signal the end of a turn", Annotations: additive,
		Description: `Signal that a turn ended (turn.end), by default your own. Returns {"event", "duplicate"}.`},
		func(in turnEndIn) string { return in.Session },
		func(ctx context.Context, c *core.Client, sid string, in turnEndIn) (*sessionv1.CommandResult, error) {
			return c.TurnEnd(ctx, sid, in.Participant)
		})

	sessionTool(srv, s, &sdk.Tool{Name: "signal_silence_long", Title: "Signal a long silence", Annotations: additive,
		Description: `Signal a long silence (silence.long) of the given duration. Returns {"event", "duplicate"}.`},
		func(in silenceIn) string { return in.Session },
		func(ctx context.Context, c *core.Client, sid string, in silenceIn) (*sessionv1.CommandResult, error) {
			d, err := time.ParseDuration(in.Duration)
			if err != nil || d <= 0 {
				return nil, usagef("duration must be a positive duration such as \"12s\", not %q", in.Duration)
			}
			return c.SilenceLong(ctx, sid, d)
		})

	sessionTool(srv, s, &sdk.Tool{Name: "signal_addressed", Title: "Signal that someone was addressed", Annotations: additive,
		Description: `Signal that an utterance mentioned a participant (addressed). Returns {"event", "duplicate"}.`},
		func(in addressedIn) string { return in.Session },
		func(ctx context.Context, c *core.Client, sid string, in addressedIn) (*sessionv1.CommandResult, error) {
			if blank(in.Participant) || in.UtteranceEventID == 0 {
				return nil, usagef("participant and utteranceEventId are required")
			}
			return c.Addressed(ctx, sid, in.Participant, in.UtteranceEventID)
		})

	sessionTool(srv, s, &sdk.Tool{Name: "hand_raise", Title: "Raise your hand", Annotations: additive,
		Description: `Raise your hand (hand.raised), optionally with a topic, to ask for the floor without interrupting. Returns {"event", "duplicate"}.`},
		func(in handIn) string { return in.Session },
		func(ctx context.Context, c *core.Client, sid string, in handIn) (*sessionv1.CommandResult, error) {
			return c.RaiseHand(ctx, sid, in.Topic)
		})

	sessionTool(srv, s, &sdk.Tool{Name: "minutes_update", Title: "Update the minutes", Annotations: additive,
		Description: `Replace the running minutes of the meeting (minutes.updated). Send the full new text, not a diff. Give text, or file for a local file. Returns {"event", "duplicate"}.`},
		func(in minutesIn) string { return in.Session },
		func(ctx context.Context, c *core.Client, sid string, in minutesIn) (*sessionv1.CommandResult, error) {
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

	sessionTool(srv, s, &sdk.Tool{Name: "stage_show", Title: "Show an artifact on the stage", Annotations: additive,
		Description: `Show an artifact on the stage for everyone (artifact.shown), for example the artifactId from slides_render. Returns {"event", "duplicate"}.`},
		func(in stageShowIn) string { return in.Session },
		func(ctx context.Context, c *core.Client, sid string, in stageShowIn) (*sessionv1.CommandResult, error) {
			if blank(in.ArtifactID) {
				return nil, usagef("artifactId is required")
			}
			return c.ShowArtifact(ctx, sid, in.ArtifactID)
		})

	tool(srv, s, &sdk.Tool{Name: "stage_link", Title: "Create a stage link", Annotations: additive,
		Description: `Create a read-only link to the stage of the session, for people without a client. Returns {"url", "expiresAt"}.`},
		func(ctx context.Context, c *core.Client, in sessionIn) (any, error) {
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

	tool(srv, s, &sdk.Tool{Name: "slides_render", Title: "Render slides", Annotations: additive,
		Description: `Render a slide deck into an artifact. Give the deck as title and slides, as markdown, or as a local file. Returns {"artifactId", "mediaType", "duplicate"}; show it with stage_show.`},
		func(ctx context.Context, c *core.Client, in slidesIn) (any, error) {
			n := 0
			for _, set := range []bool{len(in.Slides) > 0, in.Markdown != "", in.File != ""} {
				if set {
					n++
				}
			}
			if n != 1 {
				return nil, usagef("give exactly one of slides, markdown or file")
			}
			var req *artifactv1.RenderSlidesRequest
			var err error
			switch {
			case in.File != "":
				var b []byte
				if b, err = os.ReadFile(in.File); err != nil {
					return nil, err
				}
				req, err = core.ParseDeck(in.File, b)
			case in.Markdown != "":
				req, err = core.ParseDeckMarkdown([]byte(in.Markdown))
			default:
				req = &artifactv1.RenderSlidesRequest{Title: in.Title}
				for _, sl := range in.Slides {
					req.Slides = append(req.Slides, &artifactv1.Slide{Title: sl.Title, Bullets: sl.Bullets, Notes: sl.Notes})
				}
			}
			if err != nil {
				return nil, err
			}
			if len(req.GetSlides()) == 0 {
				return nil, usagef("the deck has no slides")
			}
			res, err := c.RenderSlides(ctx, req)
			if err != nil {
				return nil, err
			}
			return map[string]any{"artifactId": res.GetArtifact().GetId(), "mediaType": res.GetArtifact().GetMediaType(),
				"duplicate": res.GetDuplicate()}, nil
		})

	tool(srv, s, &sdk.Tool{Name: "artifact_get", Title: "Get an artifact", Annotations: readOnly,
		Description: `Load an artifact by its ID. Returns {"artifact": {"id", "mediaType", "content" (base64), …}}, or with out {"artifactId", "mediaType", "file"}.`},
		func(ctx context.Context, c *core.Client, in artifactIn) (any, error) {
			if blank(in.ID) {
				return nil, usagef("id is required")
			}
			a, err := c.GetArtifact(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if in.Out != "" {
				if err := os.WriteFile(in.Out, a.GetContent(), 0o600); err != nil {
					return nil, err
				}
				return map[string]any{"artifactId": a.GetId(), "mediaType": a.GetMediaType(), "file": in.Out}, nil
			}
			return map[string]any{"artifact": core.MessageJSON(a)}, nil
		})

	tool(srv, s, &sdk.Tool{Name: "journal_append", Title: "Write a journal entry", Annotations: additive,
		Description: `Write an entry into the shared journal: a decision, task, minutes or note. Returns {"entry", "duplicate"}.`},
		func(ctx context.Context, c *core.Client, in journalAppendIn) (any, error) {
			k, err := core.ParseEntryKind(in.Kind)
			if err != nil {
				return nil, err
			}
			if k == 0 {
				return nil, usagef("kind is required: decision, task, minutes or note")
			}
			if blank(in.Text) {
				return nil, usagef("text is required")
			}
			res, err := c.AppendJournal(ctx, k, in.Text, in.Session)
			if err != nil {
				return nil, err
			}
			return map[string]any{"entry": core.MessageJSON(res.GetEntry()), "duplicate": res.GetDuplicate()}, nil
		})

	tool(srv, s, &sdk.Tool{Name: "journal_list", Title: "List journal entries", Annotations: readOnly,
		Description: `List journal entries. Page with afterEntryId (the last ID you have). Returns {"entries": […]}.`},
		func(ctx context.Context, c *core.Client, in journalListIn) (any, error) {
			k, err := core.ParseEntryKind(in.Kind)
			if err != nil {
				return nil, err
			}
			entries, err := c.ListJournal(ctx, &journalv1.ListEntriesRequest{AfterEntryId: in.AfterEntryID, Kind: k,
				SessionId: in.Session, Limit: in.Limit})
			if err != nil {
				return nil, err
			}
			out := make([]any, len(entries))
			for i, en := range entries {
				out[i] = core.MessageJSON(en)
			}
			return map[string]any{"entries": out}, nil
		})

	tool(srv, s, &sdk.Tool{Name: "meeting_events", Title: "Read events after a cursor", Annotations: readOnly,
		Description: `Read the events of a session after a cursor from the server: {"events": […], "more": bool}. Event IDs grow by one without gaps. Use this when you see a gap in the IDs of your log (for example 43 after 41: read with after 41), or poll it with your last ID if you cannot follow a file. An utterance with "offTheRecord": true is a placeholder: its speaker marked it off the record. Do not use or repeat such a sentence anywhere, not in minutes and not in the journal.`},
		func(ctx context.Context, c *core.Client, in eventsIn) (any, error) {
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
				out[i] = core.EventJSON(ev)
			}
			return map[string]any{"events": out, "more": more}, nil
		})

	tool(srv, s, &sdk.Tool{Name: "whoami", Title: "Who am I", Annotations: readOnly,
		Description: `Authenticate and return your participant ID and key ID: {"participantId", "keyId"}. Use the participant ID to find your own lines in the log.`},
		func(ctx context.Context, c *core.Client, _ noIn) (any, error) {
			pid, err := c.ParticipantID(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"participantId": pid, "keyId": auth.KeyID(auth.PublicKey(c.Key))}, nil
		})
}
