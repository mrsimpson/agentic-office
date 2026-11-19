package portal

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/portal/web"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	"github.com/mrsimpson/agentic-office/hub/records/decision/api/decisionv1connect"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	"github.com/mrsimpson/agentic-office/hub/records/minutes/api/minutesv1connect"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	"github.com/mrsimpson/agentic-office/hub/records/note/api/notev1connect"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/sessionstreamv1connect"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
	"github.com/mrsimpson/agentic-office/hub/tasks/api/taskv1connect"
)

// markdown renders minutes, decisions and notes. Raw HTML and unsafe links are left out.
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

func markdownHTML(s string) template.HTML {
	var b bytes.Buffer
	if err := markdown.Convert([]byte(s), &b); err != nil {
		return template.HTML(template.HTMLEscapeString(s))
	}
	return template.HTML(b.String()) // goldmark leaves out raw HTML and unsafe links
}

const timeLayout = "2006-01-02 15:04"

// sessionView is one session as the page shows it, built from its log and its records.
type sessionView struct {
	SessionId, Started, Ended string
	Running                   bool
	Source, ImportTitle       string // set for an imported session
	Importer                  string
	Participants              []participantChip
	Lines                     []lineView
	Minutes                   template.HTML
	MinutesRunning            bool // the minutes are the running minutes of the log, not stored ones
	Decisions, Notes          []recordView
	Tasks                     []taskView
}

type lineView struct {
	EventId          uint32
	Time, Speaker    string
	SpeakerId        string
	Agent, Corrected bool
	Text             string
}

type recordView struct {
	Id     uint32
	Author string
	Time   string
	Text   template.HTML
}

type taskView struct {
	Ref, Title, Status, Assignee string
}

// session serves the page of one session. The session stream decides who may read it; the
// records of the session come from their interfaces, filtered by the session (dec-web-portal).
func (h *Handler) session(w http.ResponseWriter, r *http.Request, c *backend, tenant, escaped string) {
	sid, err := url.PathUnescape(escaped)
	if err != nil || sid == "" {
		h.fail(w, http.StatusNotFound, "Not found", "There is no such session for you.")
		return
	}
	ck, _ := r.Cookie(web.CookieName)
	cc := connect.NewClient(h.opts.Hub, auth.StaticToken(ck.Value), auth.Tenant(tenant))
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// The log, in pages. A sentence off the record comes as a placeholder without text.
	stream := sessionstreamv1connect.NewSessionStreamServiceClient(cc)
	var evs []*sessionstreamv1.Event
	for after := uint32(0); ; {
		res, err := stream.GetEvents(ctx, &sessionstreamv1.GetEventsRequest{SessionId: sid, AfterEventId: after, Limit: 1000})
		if err != nil {
			h.denied(w, err)
			return
		}
		evs = append(evs, res.GetEvents()...)
		if !res.GetMore() || len(res.GetEvents()) == 0 {
			break
		}
		after = res.GetEvents()[len(res.GetEvents())-1].GetId()
	}
	if len(evs) == 0 {
		h.fail(w, http.StatusNotFound, "Not found", "There is no such session for you.")
		return
	}
	v := viewOf(sid, evs)

	// The records of the session.
	minutes, err := listAll(func(after uint32) ([]*minutesv1.Minutes, error) {
		res, err := minutesv1connect.NewMinutesServiceClient(cc).ListMinutes(ctx, &minutesv1.ListMinutesRequest{AfterId: after, SessionId: sid, Limit: 100})
		return res.GetMinutes(), err
	}, (*minutesv1.Minutes).GetId)
	if err != nil {
		h.denied(w, err)
		return
	}
	if len(minutes) > 0 {
		v.Minutes, v.MinutesRunning = markdownHTML(minutes[len(minutes)-1].GetText()), false
	}
	decisions, err := listAll(func(after uint32) ([]*decisionv1.Decision, error) {
		res, err := decisionv1connect.NewDecisionServiceClient(cc).ListDecisions(ctx, &decisionv1.ListDecisionsRequest{AfterId: after, SessionId: sid, Limit: 100})
		return res.GetDecisions(), err
	}, (*decisionv1.Decision).GetId)
	if err != nil {
		h.denied(w, err)
		return
	}
	for _, d := range decisions {
		v.Decisions = append(v.Decisions, recordView{Id: d.GetId(), Author: d.GetAuthorParticipantId(),
			Time: d.GetTime().AsTime().UTC().Format(timeLayout), Text: markdownHTML(d.GetText())})
	}
	notes, err := listAll(func(after uint32) ([]*notev1.Note, error) {
		res, err := notev1connect.NewNoteServiceClient(cc).ListNotes(ctx, &notev1.ListNotesRequest{AfterId: after, SessionId: sid, Limit: 100})
		return res.GetNotes(), err
	}, (*notev1.Note).GetId)
	if err != nil {
		h.denied(w, err)
		return
	}
	for _, n := range notes {
		v.Notes = append(v.Notes, recordView{Id: n.GetId(), Author: n.GetAuthorParticipantId(),
			Time: n.GetTime().AsTime().UTC().Format(timeLayout), Text: markdownHTML(n.GetText())})
	}
	tasks := taskv1connect.NewTaskServiceClient(cc)
	for token := ""; ; {
		res, err := tasks.ListTasks(ctx, &taskv1.ListTasksRequest{SessionId: sid, PageToken: token})
		if err != nil {
			h.denied(w, err)
			return
		}
		for _, t := range res.GetTasks() {
			v.Tasks = append(v.Tasks, taskView{Ref: t.GetRef(), Title: t.GetTitle(), Status: statusName(t.GetStatus()),
				Assignee: t.GetAssigneeParticipantId()})
		}
		if token = res.GetNextPageToken(); token == "" {
			break
		}
	}
	h.page(w, c.me, web.TenantPath(tenant), tenant, map[string]any{"Title": sid, "Section": "Session", "Mode": "session",
		"Session": v, "Back": web.TenantPath(tenant)})
}

// listAll reads every page of a list of records that pages by the ID of the last record.
func listAll[T any](page func(after uint32) ([]T, error), id func(T) uint32) ([]T, error) {
	var all []T
	for after := uint32(0); ; {
		got, err := page(after)
		if err != nil {
			return nil, err
		}
		all = append(all, got...)
		if len(got) == 0 {
			return all, nil
		}
		after = id(got[len(got)-1])
	}
}

func statusName(s taskv1.TaskStatus) string {
	switch s {
	case taskv1.TaskStatus_TASK_STATUS_OPEN:
		return "open"
	case taskv1.TaskStatus_TASK_STATUS_IN_PROGRESS:
		return "in progress"
	case taskv1.TaskStatus_TASK_STATUS_DONE:
		return "done"
	case taskv1.TaskStatus_TASK_STATUS_CANCELLED:
		return "cancelled"
	}
	return "unknown"
}

// viewOf builds the header, the transcript and the running minutes from the log. A sentence off
// the record is a placeholder without text and does not appear; a correction replaces the text.
func viewOf(sid string, evs []*sessionstreamv1.Event) sessionView {
	v := sessionView{SessionId: sid, Started: evs[0].GetTime().AsTime().UTC().Format(timeLayout), Running: true}
	names := map[string]participantChip{}
	lines := map[uint32]int{} // event ID of an utterance -> index in v.Lines
	var minutes string
	for _, ev := range evs {
		switch p := ev.GetPayload().(type) {
		case *sessionstreamv1.Event_SessionImported:
			v.Source, v.ImportTitle, v.Importer = p.SessionImported.GetSource(), p.SessionImported.GetTitle(), ev.GetActorParticipantId()
		case *sessionstreamv1.Event_ParticipantJoined:
			j := p.ParticipantJoined
			if _, seen := names[j.GetParticipantId()]; seen {
				continue
			}
			name := j.GetDisplayName()
			if name == "" {
				name = j.GetParticipantId()
			}
			chip := participantChip{ParticipantId: j.GetParticipantId(), Name: name,
				Agent: j.GetKind() == sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT, Imported: j.GetImported()}
			names[j.GetParticipantId()] = chip
			v.Participants = append(v.Participants, chip)
		case *sessionstreamv1.Event_UtteranceFinal:
			u := p.UtteranceFinal
			if u.GetOffTheRecord() {
				continue
			}
			at := ev.GetTime()
			if u.GetOriginalTime() != nil {
				at = u.GetOriginalTime()
			}
			who := names[u.GetParticipantId()]
			if who.Name == "" {
				who.Name = u.GetParticipantId()
			}
			lines[ev.GetId()] = len(v.Lines)
			v.Lines = append(v.Lines, lineView{EventId: ev.GetId(), Time: at.AsTime().UTC().Format("15:04:05"), Speaker: who.Name,
				SpeakerId: u.GetParticipantId(), Agent: who.Agent, Text: u.GetText()})
		case *sessionstreamv1.Event_UtteranceCorrected:
			if i, ok := lines[p.UtteranceCorrected.GetUtteranceEventId()]; ok {
				v.Lines[i].Text, v.Lines[i].Corrected = p.UtteranceCorrected.GetText(), true
			}
		case *sessionstreamv1.Event_UtteranceOffTheRecord:
			// The hub sends the original as a placeholder already; drop a line that came before.
			if i, ok := lines[p.UtteranceOffTheRecord.GetUtteranceEventId()]; ok {
				v.Lines[i].Text = ""
			}
		case *sessionstreamv1.Event_MinutesUpdated:
			minutes = p.MinutesUpdated.GetText()
		case *sessionstreamv1.Event_SessionEnded:
			v.Running, v.Ended = false, ev.GetTime().AsTime().UTC().Format(timeLayout)
		}
	}
	kept := v.Lines[:0]
	for _, l := range v.Lines {
		if l.Text != "" {
			kept = append(kept, l)
		}
	}
	v.Lines = kept
	if minutes != "" {
		v.Minutes, v.MinutesRunning = markdownHTML(minutes), true
	}
	return v
}
