package contract

// Contract tests of the records (bb-records) through their three interfaces: decisions
// (if-decision-api), minutes (if-minutes-api) and notes (if-note-api), and of the minutes that
// the meeting session stores when a session ends (rs-session-ends). They start the real
// office-hub, use only the Connect clients, and read the archive repository with git.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"go.yaml.in/yaml/v3"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// recordDecision records a decision as a and fails the test on an error.
func recordDecision(t *testing.T, a *contracttest.Actor, text, sessionID string) *decisionv1.Decision {
	t.Helper()
	res, err := a.Decision.RecordDecision(ctx10(t), &decisionv1.RecordDecisionRequest{CommandId: ids.NewCommandID(),
		Text: text, AuthorParticipantId: a.ParticipantID, SessionId: sessionID})
	if err != nil {
		t.Fatalf("record %q as %s: %v", text, a.ParticipantID, err)
	}
	if res.GetDuplicate() {
		t.Fatalf("record %q: duplicate on the first call", text)
	}
	return res.GetDecision()
}

// addNote adds a note as a and fails the test on an error.
func addNote(t *testing.T, a *contracttest.Actor, text, sessionID string) *notev1.Note {
	t.Helper()
	res, err := a.Note.AddNote(ctx10(t), &notev1.AddNoteRequest{CommandId: ids.NewCommandID(),
		Text: text, AuthorParticipantId: a.ParticipantID, SessionId: sessionID})
	if err != nil {
		t.Fatalf("add %q as %s: %v", text, a.ParticipantID, err)
	}
	if res.GetDuplicate() {
		t.Fatalf("add %q: duplicate on the first call", text)
	}
	return res.GetNote()
}

func listDecisions(t *testing.T, a *contracttest.Actor, req *decisionv1.ListDecisionsRequest) []*decisionv1.Decision {
	t.Helper()
	res, err := a.Decision.ListDecisions(ctx10(t), req)
	if err != nil {
		t.Fatalf("list decisions %v: %v", req, err)
	}
	return res.GetDecisions()
}

func listNotes(t *testing.T, a *contracttest.Actor, req *notev1.ListNotesRequest) []*notev1.Note {
	t.Helper()
	res, err := a.Note.ListNotes(ctx10(t), req)
	if err != nil {
		t.Fatalf("list notes %v: %v", req, err)
	}
	return res.GetNotes()
}

func listMinutes(t *testing.T, a *contracttest.Actor, req *minutesv1.ListMinutesRequest) []*minutesv1.Minutes {
	t.Helper()
	res, err := a.Minutes.ListMinutes(ctx10(t), req)
	if err != nil {
		t.Fatalf("list minutes %v: %v", req, err)
	}
	return res.GetMinutes()
}

// withID is a record with an ID.
type withID interface{ GetId() uint32 }

func wantIDs[T withID](t *testing.T, what string, rs []T, want ...uint32) {
	t.Helper()
	got := make([]uint32, len(rs))
	for i, r := range rs {
		got[i] = r.GetId()
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s: IDs %v, want %v", what, got, want)
	}
}

// archivedYAML reads a file from the current commit of the archive and parses it as YAML.
func archivedYAML(t *testing.T, srv *contracttest.Hub, path string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := yaml.Unmarshal([]byte(contracttest.Git(t, srv.ArchiveDir(), "show", "HEAD:"+path)), &v); err != nil {
		t.Fatalf("%s in the archive: %v", path, err)
	}
	return v
}

// commitsOf returns how many commits changed path in the archive.
func commitsOf(t *testing.T, srv *contracttest.Hub, path string) int {
	t.Helper()
	return len(strings.Fields(contracttest.Git(t, srv.ArchiveDir(), "log", "--format=%H", "--", path)))
}

// TestDecisionsRecordAndList: decisions keep their text, record the author and its kind from the
// token, get growing IDs and a time, and come back oldest first, filtered by session and paged
// by the cursor.
func TestDecisionsRecordAndList(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bot := srv.Participant(t, "bot", "Bot", agent)

	before := time.Now().Add(-time.Second)
	d1 := recordDecision(t, ada, "We ship on Friday.", "s1")
	d2 := recordDecision(t, bot, "Line one\nline two", "")
	d3 := recordDecision(t, bot, "We use Go.", "s1")
	for _, w := range []struct {
		d      *decisionv1.Decision
		id     uint32
		author string
		kind   decisionv1.AuthorKind
		sid    string
	}{
		{d1, 1, "ada", decisionv1.AuthorKind_AUTHOR_KIND_HUMAN, "s1"},
		{d2, 2, "bot", decisionv1.AuthorKind_AUTHOR_KIND_AGENT, ""},
		{d3, 3, "bot", decisionv1.AuthorKind_AUTHOR_KIND_AGENT, "s1"},
	} {
		if w.d.GetId() != w.id || w.d.GetAuthorParticipantId() != w.author || w.d.GetAuthorKind() != w.kind ||
			w.d.GetSessionId() != w.sid || w.d.GetCommandId() == "" {
			t.Fatalf("decision %d: %v", w.id, w.d)
		}
		if tm := w.d.GetTime().AsTime(); tm.Before(before) || tm.After(time.Now().Add(time.Second)) {
			t.Fatalf("decision %d: time %v", w.id, tm)
		}
	}
	if d2.GetText() != "Line one\nline two" {
		t.Fatalf("text: %q", d2.GetText())
	}
	for _, a := range []*contracttest.Actor{ada, bot} {
		got := listDecisions(t, a, &decisionv1.ListDecisionsRequest{})
		wantIDs(t, "list as "+a.ParticipantID, got, 1, 2, 3)
		for i, d := range []*decisionv1.Decision{d1, d2, d3} {
			if got[i].String() != d.String() {
				t.Fatalf("listed decision %d: %v, want %v", i+1, got[i], d)
			}
		}
	}
	for _, c := range []struct {
		name string
		req  *decisionv1.ListDecisionsRequest
		want []uint32
	}{
		{"session s1", &decisionv1.ListDecisionsRequest{SessionId: "s1"}, []uint32{1, 3}},
		{"unknown session", &decisionv1.ListDecisionsRequest{SessionId: "nope"}, nil},
		{"after 1", &decisionv1.ListDecisionsRequest{AfterId: 1}, []uint32{2, 3}},
		{"after 1, limit 1", &decisionv1.ListDecisionsRequest{AfterId: 1, Limit: 1}, []uint32{2}},
		{"after 1, session s1", &decisionv1.ListDecisionsRequest{AfterId: 1, SessionId: "s1"}, []uint32{3}},
		{"after the last", &decisionv1.ListDecisionsRequest{AfterId: 3}, nil},
	} {
		wantIDs(t, c.name, listDecisions(t, ada, c.req), c.want...)
	}

	// Every decision is its own YAML file in the archive, written in its own commit, with the
	// fields of the contract.
	y := archivedYAML(t, srv, "decisions/000002.yaml")
	if y["id"] != 2 || y["text"] != "Line one\nline two" || y["author_participant_id"] != "bot" ||
		y["author_kind"] != "AUTHOR_KIND_AGENT" || y["session_id"] != "" || y["command_id"] != d2.GetCommandId() || y["time"] == nil {
		t.Fatalf("decisions/000002.yaml: %v", y)
	}
	if raw := contracttest.Git(t, srv.ArchiveDir(), "show", "HEAD:decisions/000002.yaml"); !strings.Contains(raw, "text: |-\n    Line one\n    line two\n") {
		t.Errorf("a text with line breaks is not a literal block:\n%s", raw)
	}
	for _, p := range []string{"decisions/000001.yaml", "decisions/000002.yaml", "decisions/000003.yaml"} {
		if n := commitsOf(t, srv, p); n != 1 {
			t.Fatalf("%s: %d commits, want 1", p, n)
		}
	}
	if n := commitsOf(t, srv, "decisions"); n != 3 {
		t.Fatalf("decisions: %d commits, want one per decision", n)
	}
}

// TestNotesAddAndList: notes work like decisions, in their own list with their own IDs. Without
// a limit the hub returns at most 100 notes, and never more than 1000.
func TestNotesAddAndList(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bot := srv.Participant(t, "bot", "Bot", agent)

	recordDecision(t, ada, "A decision does not count as a note.", "")
	n1 := addNote(t, bot, "Remember the budget.", "s1")
	n2 := addNote(t, ada, "Without a session.", "")
	if n1.GetId() != 1 || n1.GetAuthorParticipantId() != "bot" || n1.GetAuthorKind() != notev1.AuthorKind_AUTHOR_KIND_AGENT ||
		n1.GetSessionId() != "s1" || n1.GetTime() == nil {
		t.Fatalf("note 1: %v", n1)
	}
	if n2.GetId() != 2 || n2.GetAuthorKind() != notev1.AuthorKind_AUTHOR_KIND_HUMAN || n2.GetSessionId() != "" {
		t.Fatalf("note 2: %v", n2)
	}
	wantIDs(t, "all notes", listNotes(t, ada, &notev1.ListNotesRequest{}), 1, 2)
	wantIDs(t, "notes of s1", listNotes(t, ada, &notev1.ListNotesRequest{SessionId: "s1"}), 1)
	wantIDs(t, "notes after 1", listNotes(t, ada, &notev1.ListNotesRequest{AfterId: 1}), 2)
	if y := archivedYAML(t, srv, "notes/000001.yaml"); y["text"] != "Remember the budget." || y["session_id"] != "s1" ||
		y["author_participant_id"] != "bot" {
		t.Fatalf("notes/000001.yaml: %v", y)
	}

	for i := range 103 {
		addNote(t, bot, fmt.Sprintf("note %d", i+3), "")
	}
	if n := len(listNotes(t, bot, &notev1.ListNotesRequest{})); n != 100 {
		t.Fatalf("default limit: %d notes, want 100", n)
	}
	if n := len(listNotes(t, bot, &notev1.ListNotesRequest{Limit: 5000})); n != 105 {
		t.Fatalf("limit 5000: %d notes, want 105", n)
	}
	// Paging with the last ID as the cursor reads every note exactly once.
	var got []*notev1.Note
	var cursor uint32
	for {
		page := listNotes(t, bot, &notev1.ListNotesRequest{AfterId: cursor, Limit: 40})
		if len(page) == 0 {
			break
		}
		got = append(got, page...)
		cursor = page[len(page)-1].GetId()
	}
	if len(got) != 105 || got[104].GetId() != 105 {
		t.Fatalf("paged notes: %d, last %v", len(got), got[len(got)-1])
	}
}

// TestRecordsIdempotent: a repeated command_id returns the stored record; reusing it for another
// record fails. Each record type has its own command IDs.
func TestRecordsIdempotent(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bot := srv.Participant(t, "bot", "Bot", agent)

	dreq := &decisionv1.RecordDecisionRequest{CommandId: ids.NewCommandID(), Text: "We ship.", AuthorParticipantId: "ada", SessionId: "s1"}
	first, err := ada.Decision.RecordDecision(ctx10(t), dreq)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		again, err := ada.Decision.RecordDecision(ctx10(t), dreq)
		if err != nil {
			t.Fatal(err)
		}
		if !again.GetDuplicate() || again.GetDecision().String() != first.GetDecision().String() {
			t.Fatalf("repeat: %v, want the first decision with duplicate", again)
		}
	}
	otherD := func(change func(r *decisionv1.RecordDecisionRequest)) *decisionv1.RecordDecisionRequest {
		r := &decisionv1.RecordDecisionRequest{CommandId: dreq.GetCommandId(), Text: dreq.GetText(),
			AuthorParticipantId: dreq.GetAuthorParticipantId(), SessionId: dreq.GetSessionId()}
		change(r)
		return r
	}
	for name, r := range map[string]*decisionv1.RecordDecisionRequest{
		"other text":    otherD(func(r *decisionv1.RecordDecisionRequest) { r.Text = "We wait." }),
		"other session": otherD(func(r *decisionv1.RecordDecisionRequest) { r.SessionId = "s2" }),
	} {
		_, err := ada.Decision.RecordDecision(ctx10(t), r)
		wantCode(t, "decision with "+name, err, connect.CodeAlreadyExists)
	}
	_, err = bot.Decision.RecordDecision(ctx10(t), otherD(func(r *decisionv1.RecordDecisionRequest) { r.AuthorParticipantId = "bot" }))
	wantCode(t, "decision with another author", err, connect.CodeAlreadyExists)
	wantIDs(t, "decisions", listDecisions(t, ada, &decisionv1.ListDecisionsRequest{}), 1)
	if n := commitsOf(t, srv, "decisions"); n != 1 {
		t.Fatalf("repeats made %d commits, want 1", n)
	}

	// The same command ID for a note is a new note: the types are separate.
	nreq := &notev1.AddNoteRequest{CommandId: dreq.GetCommandId(), Text: "We ship.", AuthorParticipantId: "ada"}
	n1, err := ada.Note.AddNote(ctx10(t), nreq)
	if err != nil || n1.GetDuplicate() || n1.GetNote().GetId() != 1 {
		t.Fatalf("note with the command ID of a decision: %v, %v", n1, err)
	}
	n2, err := ada.Note.AddNote(ctx10(t), nreq)
	if err != nil || !n2.GetDuplicate() || n2.GetNote().String() != n1.GetNote().String() {
		t.Fatalf("repeated note: %v, %v", n2, err)
	}
	nreq.Text = "Other"
	_, err = ada.Note.AddNote(ctx10(t), nreq)
	wantCode(t, "note with other text", err, connect.CodeAlreadyExists)
	wantIDs(t, "notes", listNotes(t, ada, &notev1.ListNotesRequest{}), 1)
}

// TestRecordsErrors: bad requests, a wrong author, no token and stage links.
func TestRecordsErrors(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	okD := func() *decisionv1.RecordDecisionRequest {
		return &decisionv1.RecordDecisionRequest{CommandId: ids.NewCommandID(), Text: "x", AuthorParticipantId: "ada"}
	}
	okN := func() *notev1.AddNoteRequest {
		return &notev1.AddNoteRequest{CommandId: ids.NewCommandID(), Text: "x", AuthorParticipantId: "ada"}
	}
	for name, change := range map[string]func(*decisionv1.RecordDecisionRequest){
		"no command_id": func(r *decisionv1.RecordDecisionRequest) { r.CommandId = "" },
		"no text":       func(r *decisionv1.RecordDecisionRequest) { r.Text = "" },
		"blank text":    func(r *decisionv1.RecordDecisionRequest) { r.Text = " \n\t" },
		"no author":     func(r *decisionv1.RecordDecisionRequest) { r.AuthorParticipantId = "" },
	} {
		r := okD()
		change(r)
		_, err := ada.Decision.RecordDecision(ctx10(t), r)
		wantCode(t, "decision with "+name, err, connect.CodeInvalidArgument)
		n := okN()
		n.CommandId, n.Text, n.AuthorParticipantId = r.CommandId, r.Text, r.AuthorParticipantId
		_, err = ada.Note.AddNote(ctx10(t), n)
		wantCode(t, "note with "+name, err, connect.CodeInvalidArgument)
	}
	_, err := ada.Minutes.GetMinutes(ctx10(t), &minutesv1.GetMinutesRequest{})
	wantCode(t, "minutes without a session", err, connect.CodeInvalidArgument)
	_, err = ada.Minutes.GetMinutes(ctx10(t), &minutesv1.GetMinutesRequest{SessionId: "nope"})
	wantCode(t, "minutes of an unknown session", err, connect.CodeNotFound)

	d := okD()
	d.AuthorParticipantId = "bob"
	_, err = ada.Decision.RecordDecision(ctx10(t), d)
	wantCode(t, "decision of another author", err, connect.CodePermissionDenied)
	n := okN()
	n.AuthorParticipantId = "bob"
	_, err = ada.Note.AddNote(ctx10(t), n)
	wantCode(t, "note of another author", err, connect.CodePermissionDenied)

	link, err := ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: "s1", CommandId: ids.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		cl   contracttest.Clients
		code connect.Code
	}{
		"without token":     {srv.Anonymous(t), connect.CodeUnauthenticated},
		"with a bad token":  {srv.WithToken(t, "not-a-token"), connect.CodeUnauthenticated},
		"with a stage link": {srv.WithToken(t, stageToken(t, link.GetUrl())), connect.CodePermissionDenied},
	} {
		_, err := c.cl.Decision.RecordDecision(ctx10(t), okD())
		wantCode(t, "record a decision "+name, err, c.code)
		_, err = c.cl.Decision.ListDecisions(ctx10(t), &decisionv1.ListDecisionsRequest{})
		wantCode(t, "list decisions "+name, err, c.code)
		_, err = c.cl.Note.AddNote(ctx10(t), okN())
		wantCode(t, "add a note "+name, err, c.code)
		_, err = c.cl.Note.ListNotes(ctx10(t), &notev1.ListNotesRequest{})
		wantCode(t, "list notes "+name, err, c.code)
		_, err = c.cl.Minutes.GetMinutes(ctx10(t), &minutesv1.GetMinutesRequest{SessionId: "s1"})
		wantCode(t, "get minutes "+name, err, c.code)
		_, err = c.cl.Minutes.ListMinutes(ctx10(t), &minutesv1.ListMinutesRequest{})
		wantCode(t, "list minutes "+name, err, c.code)
	}

	if ds := listDecisions(t, ada, &decisionv1.ListDecisionsRequest{}); len(ds) != 0 {
		t.Fatalf("failed calls stored decisions: %v", ds)
	}
	if ns := listNotes(t, ada, &notev1.ListNotesRequest{}); len(ns) != 0 {
		t.Fatalf("failed calls stored notes: %v", ns)
	}
}

// TestRecordsSurviveRestart: the hub reads the records back from the archive when it starts.
// IDs and idempotency go on after the restart.
func TestRecordsSurviveRestart(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bot := srv.Participant(t, "bot", "Bot", agent)
	d1 := recordDecision(t, ada, "We ship.", "s1")
	d2 := recordDecision(t, bot, "Line one\nline two\n\n  indented: yes # not a comment\n", "")
	n1 := addNote(t, bot, "123", "")
	sid := "s-" + ids.NewCommandID()
	if _, err := bot.Session.Join(ctx10(t), &sessionv1.JoinRequest{SessionId: sid, CommandId: ids.NewCommandID()}); err != nil {
		t.Fatal(err)
	}
	if _, err := bot.Session.UpdateMinutes(ctx10(t), &sessionv1.UpdateMinutesRequest{SessionId: sid, CommandId: ids.NewCommandID(),
		ParticipantId: "bot", Text: "## Minutes\n- We ship."}); err != nil {
		t.Fatal(err)
	}
	if _, err := bot.Session.EndSession(ctx10(t), &sessionv1.EndSessionRequest{SessionId: sid, CommandId: ids.NewCommandID(),
		ParticipantId: "bot"}); err != nil {
		t.Fatal(err)
	}
	m1 := listMinutes(t, ada, &minutesv1.ListMinutesRequest{})
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}

	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	ada = srv.WithKey(t, ada.Key)
	if got := listDecisions(t, ada, &decisionv1.ListDecisionsRequest{}); len(got) != 2 || got[0].String() != d1.String() || got[1].String() != d2.String() {
		t.Fatalf("decisions after restart: %v, want %v and %v", got, d1, d2)
	}
	if got := listNotes(t, ada, &notev1.ListNotesRequest{}); len(got) != 1 || got[0].String() != n1.String() {
		t.Fatalf("notes after restart: %v, want %v", got, n1)
	}
	if got := listMinutes(t, ada, &minutesv1.ListMinutesRequest{}); len(got) != 1 || len(m1) != 1 || got[0].String() != m1[0].String() {
		t.Fatalf("minutes after restart: %v, want %v", got, m1)
	}
	res, err := ada.Minutes.GetMinutes(ctx10(t), &minutesv1.GetMinutesRequest{SessionId: sid})
	if err != nil || res.GetMinutes().GetText() != "## Minutes\n- We ship." {
		t.Fatalf("minutes of %s after restart: %v, %v", sid, res, err)
	}
	again, err := ada.Decision.RecordDecision(ctx10(t), &decisionv1.RecordDecisionRequest{CommandId: d1.GetCommandId(),
		Text: "We ship.", AuthorParticipantId: "ada", SessionId: "s1"})
	if err != nil || !again.GetDuplicate() || again.GetDecision().GetId() != 1 {
		t.Fatalf("repeat after restart: %v, %v", again, err)
	}
	if d3 := recordDecision(t, ada, "After the restart.", ""); d3.GetId() != 3 {
		t.Fatalf("next ID after restart: %d, want 3", d3.GetId())
	}
	if n2 := addNote(t, ada, "After the restart.", ""); n2.GetId() != 2 {
		t.Fatalf("next note ID after restart: %d, want 2", n2.GetId())
	}
	if y := archivedYAML(t, srv, "notes/000001.yaml"); y["text"] != "123" {
		t.Fatalf("a number-like text must stay a string: %v", y)
	}
}

// TestSessionEndStoresMinutes: when a session ends, the last minutes.updated text is stored as the
// minutes of that session, written by the minute taker (rs-session-ends), and committed to the
// archive before EndSession returns. A session without minutes, or with blank minutes at the
// end, stores nothing.
func TestSessionEndStoresMinutes(t *testing.T) {
	srv := contracttest.StartHub(t, nil, "-archive-interval", "1h")
	ada := srv.Participant(t, "ada", "Ada", human)
	scribe := srv.Participant(t, "scribe", "Scribe", agent)
	join := func(a *contracttest.Actor, sid string) {
		t.Helper()
		if _, err := a.Session.Join(ctx10(t), &sessionv1.JoinRequest{SessionId: sid, CommandId: ids.NewCommandID()}); err != nil {
			t.Fatalf("join %s: %v", a.ParticipantID, err)
		}
	}
	update := func(a *contracttest.Actor, sid, text string) {
		t.Helper()
		if _, err := a.Session.UpdateMinutes(ctx10(t), &sessionv1.UpdateMinutesRequest{SessionId: sid,
			CommandId: ids.NewCommandID(), ParticipantId: a.ParticipantID, Text: text}); err != nil {
			t.Fatalf("minutes of %s: %v", a.ParticipantID, err)
		}
	}
	end := func(a *contracttest.Actor, sid, cid string) *sessionv1.CommandResult {
		t.Helper()
		res, err := a.Session.EndSession(ctx10(t), &sessionv1.EndSessionRequest{SessionId: sid, CommandId: cid,
			ParticipantId: a.ParticipantID})
		if err != nil {
			t.Fatalf("end %s: %v", sid, err)
		}
		return res.GetResult()
	}

	sid := "s-" + ids.NewCommandID()
	join(ada, sid)
	join(scribe, sid)
	update(scribe, sid, "Draft")
	update(ada, sid, "Draft, fixed by Ada")
	update(scribe, sid, "## Decisions\n- We ship on Friday.")
	_, err := ada.Minutes.GetMinutes(ctx10(t), &minutesv1.GetMinutesRequest{SessionId: sid})
	wantCode(t, "minutes before the end", err, connect.CodeNotFound)

	before := time.Now().Add(-time.Second)
	endCmd := ids.NewCommandID()
	ended := end(ada, sid, endCmd)
	// The minutes are there as soon as EndSession returns, in the interface and in the archive.
	res, err := scribe.Minutes.GetMinutes(ctx10(t), &minutesv1.GetMinutesRequest{SessionId: sid})
	if err != nil {
		t.Fatal(err)
	}
	m := res.GetMinutes()
	if m.GetId() != 1 || m.GetText() != "## Decisions\n- We ship on Friday." || m.GetAuthorParticipantId() != "scribe" ||
		m.GetAuthorKind() != minutesv1.AuthorKind_AUTHOR_KIND_AGENT || m.GetSessionId() != sid || m.GetCommandId() != "office/session-end/"+endCmd {
		t.Fatalf("minutes: %v", m)
	}
	if tm := m.GetTime().AsTime(); tm.Before(before) || tm.After(ended.GetEvent().GetTime().AsTime()) {
		t.Fatalf("minutes time %v is not between %v and session.ended at %v", tm, before, ended.GetEvent().GetTime().AsTime())
	}
	if got := listMinutes(t, ada, &minutesv1.ListMinutesRequest{SessionId: sid}); len(got) != 1 || got[0].String() != m.String() {
		t.Fatalf("list of the session: %v", got)
	}
	if y := archivedYAML(t, srv, "minutes/000001.yaml"); y["session_id"] != sid || y["text"] != m.GetText() ||
		y["author_participant_id"] != "scribe" || y["author_kind"] != "AUTHOR_KIND_AGENT" || y["command_id"] != m.GetCommandId() {
		t.Fatalf("minutes/000001.yaml: %v", y)
	}

	// A repeated EndSession returns the stored event and does not store the minutes again.
	if again := end(ada, sid, endCmd); !again.GetDuplicate() || again.GetEvent().GetId() != ended.GetEvent().GetId() {
		t.Fatalf("repeated end: %v", again)
	}
	_, err = scribe.Session.EndSession(ctx10(t), &sessionv1.EndSessionRequest{SessionId: sid, CommandId: ids.NewCommandID(),
		ParticipantId: "scribe"})
	wantCode(t, "end an ended session", err, connect.CodeFailedPrecondition)
	wantIDs(t, "minutes after repeats", listMinutes(t, ada, &minutesv1.ListMinutesRequest{}), 1)
	if n := commitsOf(t, srv, "minutes"); n != 1 {
		t.Fatalf("minutes: %d commits, want 1", n)
	}

	// A session without minutes stores nothing.
	quiet := "s-" + ids.NewCommandID()
	join(ada, quiet)
	end(ada, quiet, ids.NewCommandID())
	// A session whose last minutes are blank stores nothing.
	cleared := "s-" + ids.NewCommandID()
	join(scribe, cleared)
	update(scribe, cleared, "Something")
	update(scribe, cleared, "  ")
	end(scribe, cleared, ids.NewCommandID())
	// A human minute taker is recorded as human.
	human1 := "s-" + ids.NewCommandID()
	join(ada, human1)
	update(ada, human1, "Ada's minutes")
	end(ada, human1, ids.NewCommandID())

	all := listMinutes(t, ada, &minutesv1.ListMinutesRequest{})
	wantIDs(t, "minutes of all sessions", all, 1, 2)
	if m := all[1]; m.GetSessionId() != human1 || m.GetAuthorParticipantId() != "ada" || m.GetAuthorKind() != minutesv1.AuthorKind_AUTHOR_KIND_HUMAN ||
		m.GetText() != "Ada's minutes" {
		t.Fatalf("minutes of a human: %v", m)
	}
	for _, s := range []string{quiet, cleared} {
		_, err := ada.Minutes.GetMinutes(ctx10(t), &minutesv1.GetMinutesRequest{SessionId: s})
		wantCode(t, "minutes of "+s, err, connect.CodeNotFound)
	}
}

// TestSessionEndStoresMinutesAfterRestart: session logs are not restored after a restart yet
// (#34), the minutes are. A session with the same ID after the restart stores its minutes as new
// minutes; GetMinutes returns the newest.
func TestSessionEndStoresMinutesAfterRestart(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	run := func(text string) {
		t.Helper()
		ctx := ctx10(t)
		if _, err := ada.Session.Join(ctx, &sessionv1.JoinRequest{SessionId: "standup", CommandId: ids.NewCommandID()}); err != nil {
			t.Fatal(err)
		}
		if _, err := ada.Session.UpdateMinutes(ctx, &sessionv1.UpdateMinutesRequest{SessionId: "standup",
			CommandId: ids.NewCommandID(), ParticipantId: "ada", Text: text}); err != nil {
			t.Fatal(err)
		}
		if _, err := ada.Session.EndSession(ctx, &sessionv1.EndSessionRequest{SessionId: "standup",
			CommandId: ids.NewCommandID(), ParticipantId: "ada"}); err != nil {
			t.Fatal(err)
		}
	}
	run("Monday")
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	ada = srv.WithKey(t, ada.Key)
	run("Tuesday")
	got := listMinutes(t, ada, &minutesv1.ListMinutesRequest{SessionId: "standup"})
	if len(got) != 2 || got[0].GetText() != "Monday" || got[1].GetText() != "Tuesday" {
		t.Fatalf("minutes of standup: %v", got)
	}
	res, err := ada.Minutes.GetMinutes(ctx10(t), &minutesv1.GetMinutesRequest{SessionId: "standup"})
	if err != nil || res.GetMinutes().GetText() != "Tuesday" {
		t.Fatalf("newest minutes of standup: %v, %v", res, err)
	}
}
