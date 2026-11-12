// Package tools is the contract of the MCP server (if-mcp): the instructions for the MCP client,
// the tools with their names, descriptions and hints, and their input types, from which the SDK
// builds the input schemas. The MCP server (client/mcp) implements the tools.
//
// Conventions:
//
//   - Tool names use "_" where the architecture writes "." (meeting_join for meeting.join),
//     because many MCP clients and model APIs allow only letters, digits, "_" and "-".
//   - A tool result is one JSON object, as text content and as structured content. It has the
//     same shape as the output of the matching CLI command with --json. Session commands return
//     {"event": …, "duplicate": …}, with the event in the form of its line in the local log.
//   - Errors set isError and have the form {"error":{"code":…,"message":…}} (ErrorJSON): the
//     Connect code of the server, or one of the codes here. Arguments that do not match the
//     input schema are usage errors, too.
//   - Session tools take an optional "session". Default: the --session flag or OFFICE_SESSION,
//     else the only session this participant joined from this machine.
package tools

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

// Error codes of a failed tool besides the Connect codes of the hub.
const (
	CodeUsage     = "usage"
	CodeNoKey     = "no_key"
	CodeNoSession = "no_session"
	CodeError     = "error"
)

// ErrorJSON is the result of a failed tool: {"error":{"code":…,"message":…}}.
func ErrorJSON(code, message string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code, "message": message}}
}

func ptr[T any](v T) *T { return &v }

// Instructions are sent to the MCP client when it connects. Many clients show them to the model.
const Instructions = `You take part in a meeting of the agentic office as a participant. You read what happens from a local file and act through these tools.

1. Call meeting_join with the session ID. It returns "logPath", the local session log. While this MCP server runs, it copies every event of the session into that file. The copy stops when this server stops, when you leave, or when the session ends.
2. Follow the file in the background with a filter, and react only to what matters to you. Do not wait for the tools to tell you about new events; they do not.
3. Act with the tools. Each tool answers at once. A session command returns {"event": …, "duplicate": …}: the event it created, in the same form as its line in the log. So you can find your own actions in the log.

` + LogHelp

// LogHelp explains the local log. It is part of the instructions and of the description of
// meeting_join, because not every client shows the instructions to the model.
const LogHelp = `How to read the log file:
- One JSON object per line, append-only. "id" is a number that grows by 1 per event; "type" is the dotted event type; "actorParticipantId" is who caused it; the payload is in a field named after the type in camelCase, for example {"id":7,"type":"utterance.final","actorParticipantId":"ada","utteranceFinal":{"participantId":"ada","text":"…"}}.
- Follow it in the background, for example:
  tail -n +1 -f "$LOG" | grep --line-buffered '"type":"turn.end"\|"type":"addressed"'
  or read it again from time to time and keep the last id you handled:
  jq -c 'select(.id > 41 and (.type == "utterance.final" or .type == "addressed"))' "$LOG"
- Event types: utterance.final (someone said or typed a sentence), utterance.off_the_record (the speaker marked a sentence off the record: never use or repeat that sentence, not in minutes, decisions or notes; the original line stays in your file), turn.end (a speaker finished; a good moment to answer), addressed (someone named a participant; check addressed.participantId), silence.long, hand.raised, minutes.updated, artifact.created and artifact.changed (someone wrote an artifact), artifact.focused, participant.joined, participant.left, participant.paused, participant.resumed, session.ended (the last line; the file is complete), session.imported and import.completed (the session was imported from another tool, see session_import; its speakers are not registered participants and the hub cannot check what they said).
- Usually filter for turn.end and addressed, and read the utterance.final lines before them for the context.
- IDs have no gaps. If you see a gap (43 after 41), read the missing events with meeting_events (after 41). meeting_events also works without the file, if you poll it with your last id.`

// drafts ends the description of every task tool.
const drafts = ` Tasks are local drafts in the hub for now; later the hub syncs them with the team's task tool (for example GitHub issues), and the ref then names the synced task. Treat a ref as opaque.`

// Annotations: hints for clients. Session commands change the meeting (Additive); reads do
// not (ReadOnly).
var (
	ReadOnly = &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}
	Additive = &sdk.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)}
)

// MeetingPause is the tool meeting_pause.
var MeetingPause = &sdk.Tool{Name: "meeting_pause", Title: "Pause", Annotations: Additive,
	Description: `Pause your capture (participant.paused). For humans whose speech is transcribed; an agent rarely needs it. Returns {"event", "duplicate"}.`}

// MeetingResume is the tool meeting_resume.
var MeetingResume = &sdk.Tool{Name: "meeting_resume", Title: "Resume", Annotations: Additive,
	Description: `End a pause (participant.resumed). Returns {"event", "duplicate"}.`}

// MeetingEnd is the tool meeting_end.
var MeetingEnd = &sdk.Tool{Name: "meeting_end", Title: "End the meeting for everyone", Annotations: Additive,
	Description: `End the session for all participants. session.ended becomes the last line of every log. Only do this when asked to. Returns {"event", "duplicate"}.`}

// MeetingJoin is the tool meeting_join.
var MeetingJoin = &sdk.Tool{Name: "meeting_join", Title: "Join a meeting", Annotations: Additive,
	Description: `Join a meeting session and start copying its events into a local log file. Returns {"sessionId", "participantId", "logPath", "replicatorPid", "event", "duplicate"}. Joining again is safe: you stay the same participant and get the same file. The copy runs as long as this MCP server runs (replicatorPid is the process that writes the file), until you leave or the session ends; call meeting_join again to restart it.

` + LogHelp}

// MeetingLog is the tool meeting_log.
var MeetingLog = &sdk.Tool{Name: "meeting_log", Title: "Path of the local log", Annotations: ReadOnly,
	Description: `Return the local log file of a session you joined, and whether a replicator writes it now: {"sessionId", "participantId", "logPath", "replicatorRunning"}. If replicatorRunning is false and the session has not ended, call meeting_join again.`}

// MeetingLeave is the tool meeting_leave.
var MeetingLeave = &sdk.Tool{Name: "meeting_leave", Title: "Leave the meeting", Annotations: Additive,
	Description: `Leave the session. The log file gets your participant.left line and then stops growing. Returns {"event", "duplicate"}.`}

// UtteranceSay is the tool utterance_say.
var UtteranceSay = &sdk.Tool{Name: "utterance_say", Title: "Say something", Annotations: Additive,
	Description: `Say something in the meeting (a typed utterance, utterance.final with modality text). The stage shows it, and every participant reads it in their log. Returns {"event", "duplicate"}; event.id is the ID if you want to mark it off the record later.`}

// UtteranceOffTheRecord is the tool utterance_off_the_record.
var UtteranceOffTheRecord = &sdk.Tool{Name: "utterance_off_the_record", Title: "Mark a sentence off the record", Annotations: Additive,
	Description: `Mark one of your own sentences off the record. What is said stays said: this adds an utterance.off_the_record line that points to the sentence. Readers who read the session later get only a placeholder without its text, and nobody may use the sentence in minutes, decisions or notes. You cannot undo it. Give eventId, or last=true for your newest sentence. Returns {"event", "duplicate"}.`}

// SignalTurnEnd is the tool signal_turn_end.
var SignalTurnEnd = &sdk.Tool{Name: "signal_turn_end", Title: "Signal the end of a turn", Annotations: Additive,
	Description: `Signal that a turn ended (turn.end), by default your own. Returns {"event", "duplicate"}.`}

// SignalSilenceLong is the tool signal_silence_long.
var SignalSilenceLong = &sdk.Tool{Name: "signal_silence_long", Title: "Signal a long silence", Annotations: Additive,
	Description: `Signal a long silence (silence.long) of the given duration. Returns {"event", "duplicate"}.`}

// SignalAddressed is the tool signal_addressed.
var SignalAddressed = &sdk.Tool{Name: "signal_addressed", Title: "Signal that someone was addressed", Annotations: Additive,
	Description: `Signal that an utterance mentioned a participant (addressed). Returns {"event", "duplicate"}.`}

// HandRaise is the tool hand_raise.
var HandRaise = &sdk.Tool{Name: "hand_raise", Title: "Raise your hand", Annotations: Additive,
	Description: `Raise your hand (hand.raised), optionally with a topic, to ask for the floor without interrupting. Returns {"event", "duplicate"}.`}

// MinutesUpdate is the tool minutes_update.
var MinutesUpdate = &sdk.Tool{Name: "minutes_update", Title: "Update the minutes", Annotations: Additive,
	Description: `Replace the running minutes of the meeting (minutes.updated). Send the full new text, not a diff. Never include a sentence that is off the record. When the session ends, the hub stores the last version (see minutes_get). Give text, or file for a local file. Returns {"event", "duplicate"}.`}

// StageFocus is the tool stage_focus.
var StageFocus = &sdk.Tool{Name: "stage_focus", Title: "Show an artifact on the stage", Annotations: Additive,
	Description: `Show an artifact on the stage for everyone (artifact.focused), for example the id of a deck from slides_render. Returns {"event", "duplicate"}.`}

// StageLink is the tool stage_link.
var StageLink = &sdk.Tool{Name: "stage_link", Title: "Create a stage link", Annotations: Additive,
	Description: `Create a read-only link to the stage of the session, for people without a client. Returns {"url", "expiresAt"}.`}

// SlidesRender is the tool slides_render.
var SlidesRender = &sdk.Tool{Name: "slides_render", Title: "Render slides", Annotations: Additive,
	Description: `Render a slide deck. The hub stores its input and the rendered deck. Give the deck as title and slides, as markdown, or as a local file. Returns {"id", "mediaType", "duplicate"}; show it with stage_focus (artifactId = id).`}

// DocumentCreate is the tool document_create.
var DocumentCreate = &sdk.Tool{Name: "document_create", Title: "Create a document", Annotations: Additive,
	Description: `Create a text document (Markdown) "documents/<name>.md" in the archive. The path must not exist. Returns {"path", "version", "duplicate"}. Pass session to put artifact.created into the log of a session you joined.`}

// DocumentChange is the tool document_change.
var DocumentChange = &sdk.Tool{Name: "document_change", Title: "Change a document", Annotations: Additive,
	Description: `Replace the content of a document. Give the baseVersion you read with document_get. If someone changed the document first, the call fails with aborted and gives the current version: read again and redo your change. Returns {"path", "version", "duplicate"}.`}

// DocumentGet is the tool document_get.
var DocumentGet = &sdk.Tool{Name: "document_get", Title: "Read a document", Annotations: ReadOnly,
	Description: `Read a Markdown or YAML file of the archive by its path. Returns {"path", "version", "content"}.`}

// DocumentList is the tool document_list.
var DocumentList = &sdk.Tool{Name: "document_list", Title: "List documents", Annotations: ReadOnly,
	Description: `List the Markdown and YAML files of a directory of the archive (default "documents"). Returns {"files": [{"path", "version"}]}.`}

// SlidesGet is the tool slides_get.
var SlidesGet = &sdk.Tool{Name: "slides_get", Title: "Get a slide deck", Annotations: ReadOnly,
	Description: `Load a slide deck by its ID, with its input and the rendered deck. Returns {"deck": {"id", "title", "slides", "authorParticipantId", "createTime", "mediaType", "content" (base64), …}}, or with out {"id", "mediaType", "file"}.`}

// DecisionRecord is the tool decision_record.
var DecisionRecord = &sdk.Tool{Name: "decision_record", Title: "Record a decision", Annotations: Additive,
	Description: `Record a decision of the team, for example "We ship on Friday.", with you as its author. Never put a sentence that is off the record into a decision. Give session to link it to a meeting. Returns {"decision", "duplicate"}.`}

// DecisionList is the tool decision_list.
var DecisionList = &sdk.Tool{Name: "decision_list", Title: "List decisions", Annotations: ReadOnly,
	Description: `List the recorded decisions, oldest first. Page with afterId (the last ID you have). Returns {"decisions": […]}.`}

// NoteAdd is the tool note_add.
var NoteAdd = &sdk.Tool{Name: "note_add", Title: "Add a note", Annotations: Additive,
	Description: `Add a free note, with you as its author. Never put a sentence that is off the record into a note. Give session to link it to a meeting. Returns {"note", "duplicate"}.`}

// NoteList is the tool note_list.
var NoteList = &sdk.Tool{Name: "note_list", Title: "List notes", Annotations: ReadOnly,
	Description: `List the notes, oldest first. Page with afterId (the last ID you have). Returns {"notes": […]}.`}

// MinutesGet is the tool minutes_get.
var MinutesGet = &sdk.Tool{Name: "minutes_get", Title: "Get the minutes of a session", Annotations: ReadOnly,
	Description: `Get the minutes that the hub stored when a session ended: the last version written with minutes_update. Returns {"minutes": {"sessionId", "text", "authorParticipantId", …}}; fails with not_found if the session has no stored minutes (for example while it still runs; then read the minutes.updated lines of your log). While you write minutes, never include a sentence that is off the record.`}

// MinutesList is the tool minutes_list.
var MinutesList = &sdk.Tool{Name: "minutes_list", Title: "List stored minutes", Annotations: ReadOnly,
	Description: `List the minutes of ended sessions, oldest first. Page with afterId (the last ID you have). Returns {"minutes": […]}.`}

// TaskCreate is the tool task_create.
var TaskCreate = &sdk.Tool{Name: "task_create", Title: "Create a task", Annotations: Additive,
	Description: `Create a task of the team, with you as its author, for example "Write the release notes" assigned to a participant. Give session to link it to a meeting. Never put a sentence that is off the record into a task. Returns {"task": {"ref", "status", …}, "duplicate"}.` + drafts}

// TaskGet is the tool task_get.
var TaskGet = &sdk.Tool{Name: "task_get", Title: "Get a task", Annotations: ReadOnly,
	Description: `Get one task by its ref. Returns {"task"}; fails with not_found for an unknown ref.` + drafts}

// TaskList is the tool task_list.
var TaskList = &sdk.Tool{Name: "task_list", Title: "List tasks", Annotations: ReadOnly,
	Description: `List tasks, oldest first, filtered by statuses, assignee and session. Returns {"tasks": […], "nextPageToken"}; while nextPageToken is not empty, pass it as pageToken to read the next page.` + drafts}

// TaskUpdate is the tool task_update.
var TaskUpdate = &sdk.Tool{Name: "task_update", Title: "Update a task", Annotations: Additive,
	Description: `Change a task: give its ref and only the fields to change (title, description, assignee, status, session). For example status "done" when the work is finished. Every change is kept in the history of the archive. Returns {"task", "duplicate"}; the revision of the task grows with every change.` + drafts}

// SessionImport is the tool session_import.
var SessionImport = &sdk.Tool{Name: "session_import", Title: "Import a conversation", Annotations: Additive,
	Description: `Import a whole conversation that happened outside the office (another meeting tool, a chat, an agent conversation) as a new session. The session is marked as imported: its first event is session.imported with the source, the speakers join as imported participants ("imported:<id>"), every message is an utterance.final, and import.completed ends the import. Give the conversation as {"source", "title", "participants": [{"id", "name", "kind": "human"|"agent"}], "utterances": [{"speaker", "text", "time", "modality": "text"|"voice"}]}, or as a file in that format. Returns {"sessionId", "firstEventId", "lastEventId", "participants", "utterances", "pages", "treeHead"}. Afterwards read it with meeting_events, join it with meeting_join to write minutes, and record decisions, notes and tasks with its session ID, as for any session.`}

// MeetingEvents is the tool meeting_events.
var MeetingEvents = &sdk.Tool{Name: "meeting_events", Title: "Read events after a cursor", Annotations: ReadOnly,
	Description: `Read the events of a session after a cursor from the server: {"events": […], "more": bool}. Event IDs grow by one without gaps. Use this when you see a gap in the IDs of your log (for example 43 after 41: read with after 41), or poll it with your last ID if you cannot follow a file. An utterance with "offTheRecord": true is a placeholder: its speaker marked it off the record. Do not use or repeat such a sentence anywhere, not in minutes, decisions or notes.`}

// Whoami is the tool whoami.
var Whoami = &sdk.Tool{Name: "whoami", Title: "Who am I", Annotations: ReadOnly,
	Description: `Authenticate and return your participant ID and key ID: {"participantId", "keyId"}. Use the participant ID to find your own lines in the log.`}

// JoinIn is the input of a tool.
type JoinIn struct {
	Session string `json:"session" jsonschema:"Session ID to join, for example \"weekly\"."`
	Name    string `json:"name,omitempty" jsonschema:"Display name in this session. Default: the registered name."`
}

// SessionIn is the input of a tool.
type SessionIn struct {
	Session string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

// SayIn is the input of a tool.
type SayIn struct {
	Text    string `json:"text" jsonschema:"What you say: one or a few sentences."`
	Session string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

// OffTheRecordIn is the input of a tool.
type OffTheRecordIn struct {
	EventID uint32 `json:"eventId,omitempty" jsonschema:"Event ID (the id of its utterance.final line) of your own sentence to mark off the record."`
	Last    bool   `json:"last,omitempty" jsonschema:"Mark off the record your newest sentence that is not off the record yet, read from the local log. Use either eventId or last."`
	Session string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

// TurnEndIn is the input of a tool.
type TurnEndIn struct {
	Participant string `json:"participant,omitempty" jsonschema:"Participant whose turn ended. Default: you."`
	Session     string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

// SilenceIn is the input of a tool.
type SilenceIn struct {
	Duration string `json:"duration" jsonschema:"Length of the silence, for example \"12s\" or \"1m30s\"."`
	Session  string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

// AddressedIn is the input of a tool.
type AddressedIn struct {
	Participant      string `json:"participant" jsonschema:"Participant whose name was mentioned."`
	UtteranceEventID uint32 `json:"utteranceEventId" jsonschema:"Event ID of the utterance.final that mentioned the name."`
	Session          string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

// HandIn is the input of a tool.
type HandIn struct {
	Topic   string `json:"topic,omitempty" jsonschema:"What you want to say, in a few words."`
	Session string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

// MinutesIn is the input of a tool.
type MinutesIn struct {
	Text    string `json:"text,omitempty" jsonschema:"The full new minutes (Markdown). They replace the old minutes. Use text or file."`
	File    string `json:"file,omitempty" jsonschema:"Read the minutes from this local file instead."`
	Session string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

// ImportIn is the input of a tool.
type ImportIn struct {
	File         string                `json:"file,omitempty" jsonschema:"A local file with the conversation in the import format. Use file or conversation."`
	Conversation *coreapi.Conversation `json:"conversation,omitempty" jsonschema:"The conversation itself, in the import format."`
	Session      string                `json:"session,omitempty" jsonschema:"ID of the new session. Default: a new ID. The session must not exist yet."`
}

// StageFocusIn is the input of a tool.
type StageFocusIn struct {
	ArtifactID string `json:"artifactId" jsonschema:"Artifact to show, for example the deck ID \"slides/42\" from slides_render."`
	Session    string `json:"session,omitempty" jsonschema:"Session ID. Default: the session given when the server was started, else the only session you joined from this machine."`
}

// SlideIn is the input of a tool.
type SlideIn struct {
	Title   string   `json:"title" jsonschema:"Title of the slide."`
	Bullets []string `json:"bullets,omitempty" jsonschema:"Bullet points."`
	Notes   string   `json:"notes,omitempty" jsonschema:"Speaker notes."`
}

// SlidesIn is the input of a tool.
type SlidesIn struct {
	Title    string    `json:"title,omitempty" jsonschema:"Title of the deck (with slides)."`
	Slides   []SlideIn `json:"slides,omitempty" jsonschema:"The slides. Use slides, markdown or file."`
	Markdown string    `json:"markdown,omitempty" jsonschema:"The deck in Markdown: \"# \" deck title, \"## \" starts a slide, \"- \" bullet, other text is the notes."`
	File     string    `json:"file,omitempty" jsonschema:"A local deck file: .md (Markdown as above) or .json ({\"title\", \"slides\"})."`
}

// DeckIn is the input of a tool.
type DeckIn struct {
	ID  string `json:"id" jsonschema:"Deck ID, for example \"slides/42\"."`
	Out string `json:"out,omitempty" jsonschema:"Write the rendered deck to this local file instead of returning it (base64) in the result."`
}

// DocumentIn is the input of document_create and document_change.
type DocumentIn struct {
	Path        string `json:"path" jsonschema:"Path of the document, \"documents/<name>.md\"."`
	Content     string `json:"content" jsonschema:"The Markdown text. Never put a sentence that is off the record into it."`
	BaseVersion string `json:"baseVersion,omitempty" jsonschema:"Only for document_change: the version you read. A stale version fails with aborted; read the document again."`
	Session     string `json:"session,omitempty" jsonschema:"Session whose log gets the event (optional); you must have joined it."`
}

// FileIn is the input of document_get and document_list.
type FileIn struct {
	Path string `json:"path,omitempty" jsonschema:"document_get: the path, for example \"documents/plan.md\". document_list: the directory (default \"documents\")."`
}

// RecordIn is the input of a tool.
type RecordIn struct {
	Text    string `json:"text" jsonschema:"The text. Never include a sentence that is off the record."`
	Session string `json:"session,omitempty" jsonschema:"Link the record to this session. Default: no session (unlike the session commands)."`
}

// ListIn is the input of a tool.
type ListIn struct {
	AfterID uint32 `json:"afterId,omitempty" jsonschema:"Only records with an ID greater than this (the last ID you have)."`
	Limit   uint32 `json:"limit,omitempty" jsonschema:"At most this many records. 0: the hub's default (100); at most 1000."`
	Session string `json:"session,omitempty" jsonschema:"Only records of this session. Default: all."`
}

// MinutesGetIn is the input of a tool.
type MinutesGetIn struct {
	Session string `json:"session,omitempty" jsonschema:"The ended session. Default: the session given when the server was started, else the only session you joined from this machine."`
}

// EventsIn is the input of a tool.
type EventsIn struct {
	Session string `json:"session,omitempty" jsonschema:"The session. Default: the only session you joined from this machine."`
	After   uint32 `json:"after,omitempty" jsonschema:"The cursor: only events with an ID greater than this. 0 reads from the start."`
	Limit   uint32 `json:"limit,omitempty" jsonschema:"At most this many events. 0: the server's default (100); at most 1000."`
}

// TaskCreateIn is the input of a tool.
type TaskCreateIn struct {
	Title       string `json:"title" jsonschema:"Short title of the task."`
	Description string `json:"description,omitempty" jsonschema:"What to do, as free text (Markdown). Never include a sentence that is off the record."`
	Assignee    string `json:"assignee,omitempty" jsonschema:"Participant ID of who does the task. Default: nobody yet."`
	Status      string `json:"status,omitempty" jsonschema:"open, in_progress, done or cancelled. Default: open."`
	Session     string `json:"session,omitempty" jsonschema:"Link the task to this session. Default: no session (unlike the session commands)."`
}

// TaskRefIn is the input of a tool.
type TaskRefIn struct {
	Ref string `json:"ref" jsonschema:"The ref of the task as the hub returned it, for example \"local:42\". Treat it as opaque."`
}

// TaskListIn is the input of a tool.
type TaskListIn struct {
	Statuses  []string `json:"statuses,omitempty" jsonschema:"Only tasks with one of these statuses: open, in_progress, done, cancelled. Default: all."`
	Assignee  string   `json:"assignee,omitempty" jsonschema:"Only tasks of this participant. Default: all."`
	Session   string   `json:"session,omitempty" jsonschema:"Only tasks linked to this session. Default: all."`
	PageToken string   `json:"pageToken,omitempty" jsonschema:"The nextPageToken of the previous result, to read the next page."`
	Limit     uint32   `json:"limit,omitempty" jsonschema:"At most this many tasks. 0: the hub's default (100); at most 1000."`
}

// TaskUpdateIn is the input of a tool.
type TaskUpdateIn struct {
	Ref         string  `json:"ref" jsonschema:"The ref of the task, for example \"local:42\"."`
	Title       *string `json:"title,omitempty" jsonschema:"New title."`
	Description *string `json:"description,omitempty" jsonschema:"New description. Never include a sentence that is off the record."`
	Assignee    *string `json:"assignee,omitempty" jsonschema:"New assignee (participant ID); an empty string removes the assignee."`
	Status      *string `json:"status,omitempty" jsonschema:"New status: open, in_progress, done or cancelled."`
	Session     *string `json:"session,omitempty" jsonschema:"Link the task to this session; an empty string removes the link."`
}

// NoIn is the input of a tool.
type NoIn struct{}
