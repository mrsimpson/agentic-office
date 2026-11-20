package contract

// Contract tests of the staged records (concept-session-release) through the decisions, the notes
// and the minutes (if-decision-api, if-note-api, if-minutes-api) and the records check
// (if-records-check), against the real office-hub. A record that names a session that is not
// released waits in the staging area of the session; the artifact repository holds its stub.

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	recordscheck "github.com/mrsimpson/agentic-office/hub/records/check"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// a returns the actor of a participant of the meeting.
func (m *meeting) a(pid string) *contracttest.Actor {
	m.t.Helper()
	a := m.actors[pid]
	if a == nil {
		m.t.Fatalf("no actor for participant %q", pid)
	}
	return a
}

// note adds a note as pid that names session; it fails the test on an error.
func (m *meeting) note(pid, session, text string) *notev1.Note {
	m.t.Helper()
	res, err := m.a(pid).Note.AddNote(m.ctx(), &notev1.AddNoteRequest{CommandId: coreapi.NewCommandID(), Text: text,
		AuthorParticipantId: pid, SessionId: session})
	if err != nil {
		m.t.Fatalf("add the note %q as %s: %v", text, pid, err)
	}
	return res.GetNote()
}

// notesOf lists all notes as a reads them.
func notesOf(t *testing.T, a *contracttest.Actor, session string) []*notev1.Note {
	t.Helper()
	return listNotes(t, a, &notev1.ListNotesRequest{SessionId: session})
}

// commitsAt returns "<author name>|<subject>" of every commit of the artifact repository that
// changed path, newest first.
func commitsAt(t *testing.T, srv *contracttest.Hub, path string) []string {
	t.Helper()
	out := contracttest.Git(t, srv.ArchiveDir(), "log", "--format=%an|%s", "main", "--", path)
	return strings.FieldsFunc(out, func(r rune) bool { return r == '\n' })
}

// isStub reports whether a record is the stub of record id: only the ID and stub are set.
func isStub(id uint32, r interface {
	GetId() uint32
	GetStub() bool
	GetStaged() bool
	GetSessionId() string
	GetAuthorParticipantId() string
	GetText() string
}) bool {
	return r.GetId() == id && r.GetStub() && !r.GetStaged() && r.GetSessionId() == "" && r.GetAuthorParticipantId() == "" && r.GetText() == ""
}

// TestStagedRecordsRoutingAndStubs: only a participant names a session. A record of a session
// that is not released goes into the staging area of the session, and its stub into the artifact
// repository: the same content for every stub of a type, without session or time, by the hub,
// with a commit message that says nothing about the record. A record without a session is
// released at once. The records check accepts a stub as a second form of its schema.
func TestStagedRecordsRoutingAndStubs(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	ada := m.join("Ada", kHuman)
	bot := m.join("Bot", kAgent)
	carl := m.register("Carl", kHuman).ParticipantID // a member who never joined

	n1 := m.note(ada, m.sid, "Call the bank about the loan.")
	if n1.GetId() != 1 || !n1.GetStaged() || n1.GetStub() || n1.GetSessionId() != m.sid || n1.GetText() != "Call the bank about the loan." {
		t.Fatalf("staged note: %v", n1)
	}
	d1 := recordDecision(t, m.a(bot), "We hire two people.", m.sid)
	if d1.GetId() != 1 || !d1.GetStaged() {
		t.Fatalf("staged decision: %v", d1)
	}
	n2 := m.note(bot, m.sid, "Ask Lyon for an office.")
	free := m.note(ada, "", "A note without a session.")
	if free.GetId() != 3 || free.GetStaged() {
		t.Fatalf("a note without a session is released at once: %v", free)
	}

	// Only a participant names a session; a session that does not exist gets the same answer.
	for name, sid := range map[string]string{"a session carl did not join": m.sid, "a session that does not exist": "s-nowhere"} {
		_, err := m.a(carl).Note.AddNote(m.ctx(), &notev1.AddNoteRequest{CommandId: coreapi.NewCommandID(), Text: "x",
			AuthorParticipantId: carl, SessionId: sid})
		wantCode(t, "a note of "+name, err, connect.CodePermissionDenied)
		_, err = m.a(carl).Decision.RecordDecision(m.ctx(), &decisionv1.RecordDecisionRequest{CommandId: coreapi.NewCommandID(), Text: "x",
			AuthorParticipantId: carl, SessionId: sid})
		wantCode(t, "a decision of "+name, err, connect.CodePermissionDenied)
	}
	_, err := m.a(ada).Note.AddNote(m.ctx(), &notev1.AddNoteRequest{CommandId: coreapi.NewCommandID(), Text: "x",
		AuthorParticipantId: ada, SessionId: "s-nowhere"})
	wantCode(t, "a note of a session that does not exist", err, connect.CodePermissionDenied)

	// The staging area holds the records; the artifact repository holds their stubs.
	if y, body := frontMatter(t, "notes/000001.md", stagedFile(t, m.srv, "notes/000001.md")); y["session_id"] != m.sid ||
		y["author_participant_id"] != ada || body != "Call the bank about the loan.\n" {
		t.Errorf("the staged note: %v %q", y, body)
	}
	noteStub := string(recordscheck.Stub(recordscheck.SchemaNote))
	for _, p := range []string{"notes/000001.md", "notes/000002.md"} {
		if got := m.artifact(p); got != noteStub {
			t.Errorf("%s in the artifact repository is not the stub of a note:\n%s", p, got)
		}
	}
	if got := m.artifact("decisions/000001.yaml"); got != string(recordscheck.Stub(recordscheck.SchemaDecision)) {
		t.Errorf("decisions/000001.yaml in the artifact repository is not the stub of a decision:\n%s", got)
	}
	if strings.Contains(noteStub, m.sid) || strings.Contains(noteStub, "time") || strings.Contains(noteStub, ada) {
		t.Errorf("the stub names the session, a time or an author:\n%s", noteStub)
	}
	// Every stub is written by the hub with the same message, which says nothing about the record.
	c1, c2 := commitsAt(t, m.srv, "notes/000001.md"), commitsAt(t, m.srv, "notes/000002.md")
	cd := commitsAt(t, m.srv, "decisions/000001.yaml")
	if len(c1) != 1 || len(c2) != 1 || len(cd) != 1 || c1[0] != c2[0] || c1[0] != cd[0] || !strings.HasPrefix(c1[0], "office-hub|") {
		t.Errorf("stub commits %v, %v, %v: want one each, all the same, by the hub", c1, c2, cd)
	}
	for _, s := range []string{"bank", "Lyon", "hire", m.sid, ada, bot} {
		if strings.Contains(c1[0], s) {
			t.Errorf("the commit message of a stub %q names %q", c1[0], s)
		}
	}
	// A released note has the start of its text in its commit message.
	if c := commitsAt(t, m.srv, "notes/000003.md"); len(c) != 1 || !strings.Contains(c[0], "A note without a session.") {
		t.Errorf("commit of a released note: %v", c)
	}
	_ = n2

	// The records check accepts a stub, byte for byte, as a second form of each schema, and
	// refuses a record file that stores the fields staged or stub.
	for _, c := range []struct{ schema, path string }{
		{recordscheck.SchemaDecision, "decisions/000007.yaml"},
		{recordscheck.SchemaMinutes, "minutes/000007.yaml"},
		{recordscheck.SchemaNote, "notes/000007.md"},
	} {
		stub := recordscheck.Stub(c.schema)
		if len(stub) == 0 || !recordscheck.IsStub(c.schema, stub) {
			t.Fatalf("%s has no stub", c.schema)
		}
		if reasons := recordscheck.Validate(c.schema, c.path, stub); len(reasons) != 0 {
			t.Errorf("the check refuses the stub of %s: %v", c.schema, reasons)
		}
		if reasons := recordscheck.Validate(c.schema, c.path, append(stub, '\n')); len(reasons) == 0 {
			t.Errorf("the check accepts a stub of %s with another content", c.schema)
		}
	}
	staged := strings.Replace(stagedFile(t, m.srv, "notes/000001.md"), "---\n", "---\nstaged: true\n", 1)
	if reasons := recordscheck.Validate(recordscheck.SchemaNote, "notes/000001.md", []byte(staged)); len(reasons) == 0 {
		t.Error("the check accepts a note that stores the field staged")
	}
}

// TestStagedRecordsReads: the participants of the session and the tenant admins read a staged
// record; every other member sees its stub. A filter by session never returns a stub, and the
// minutes of a session that is not released are not found for a member who did not take part.
func TestStagedRecordsReads(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	admin := m.reader // the first participant is the tenant admin
	ada := m.join("Ada", kHuman)
	bot := m.join("Bot", kAgent)
	carl := m.register("Carl", kHuman).ParticipantID

	n1 := m.note(ada, m.sid, "Staged note.")
	m.note(carl, "", "Released note.")
	recordDecision(t, m.a(bot), "Staged decision.", m.sid)
	if _, err := m.a(bot).Commands.UpdateMinutes(m.ctx(), &sessioncommandsv1.UpdateMinutesRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: bot, Text: "# Minutes"}); err != nil {
		t.Fatal(err)
	}
	m.end(ada)

	for name, a := range map[string]*contracttest.Actor{"ada": m.a(ada), "bot": m.a(bot), "the tenant admin": admin} {
		got := notesOf(t, a, "")
		if len(got) != 2 || got[0].String() != n1.String() || got[1].GetStaged() || got[1].GetText() != "Released note." {
			t.Errorf("notes as %s: %v", name, got)
		}
		if got := notesOf(t, a, m.sid); len(got) != 1 || got[0].GetText() != "Staged note." {
			t.Errorf("notes of the session as %s: %v", name, got)
		}
		if ds := listDecisions(t, a, &decisionv1.ListDecisionsRequest{}); len(ds) != 1 || !ds[0].GetStaged() || ds[0].GetText() != "Staged decision." {
			t.Errorf("decisions as %s: %v", name, ds)
		}
		res, err := a.Minutes.GetMinutes(m.ctx(), &minutesv1.GetMinutesRequest{SessionId: m.sid})
		if err != nil || !res.GetMinutes().GetStaged() || res.GetMinutes().GetText() != "# Minutes" {
			t.Errorf("minutes as %s: %v, %v", name, res, err)
		}
	}

	c := m.a(carl)
	got := notesOf(t, c, "")
	if len(got) != 2 || !isStub(1, got[0]) || got[1].GetText() != "Released note." {
		t.Errorf("notes as a member who did not take part: %v", got)
	}
	if got := notesOf(t, c, m.sid); len(got) != 0 {
		t.Errorf("a filter by session returns stubs: %v", got)
	}
	if ds := listDecisions(t, c, &decisionv1.ListDecisionsRequest{}); len(ds) != 1 || !isStub(1, ds[0]) {
		t.Errorf("decisions as a member who did not take part: %v", ds)
	}
	if ms := listMinutes(t, c, &minutesv1.ListMinutesRequest{}); len(ms) != 1 || !isStub(1, ms[0]) {
		t.Errorf("minutes as a member who did not take part: %v", ms)
	}
	_, err := c.Minutes.GetMinutes(m.ctx(), &minutesv1.GetMinutesRequest{SessionId: m.sid})
	wantCode(t, "staged minutes for a member who did not take part", err, connect.CodeNotFound)
}

// TestStagedRecordsChangeRemoveAndRelease: until the release, every participant changes or
// removes a staged record. A change that names another session or none, a change by a member who
// did not take part, a change of a released record and of a stub are refused. The release moves
// the records into the artifact repository, replaces their stubs and removes the stub of a
// removed record. Repeated commands stay idempotent across the move, and a record that names the
// released session is released at once.
func TestStagedRecordsChangeRemoveAndRelease(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	ada := m.join("Ada", kHuman)
	bot := m.join("Bot", kAgent)
	carl := m.register("Carl", kHuman).ParticipantID
	other := "s-" + coreapi.NewCommandID()
	joinAll(t, other, m.a(ada))

	n1 := m.note(ada, m.sid, "Call the bank.")
	free := m.note(ada, "", "Released note.")
	d1 := recordDecision(t, m.a(bot), "We hire two people.", m.sid)
	keepReq := &notev1.AddNoteRequest{CommandId: coreapi.NewCommandID(), Text: "Kept as it is.", AuthorParticipantId: ada, SessionId: m.sid}
	kept, err := m.a(ada).Note.AddNote(m.ctx(), keepReq)
	if err != nil {
		t.Fatal(err)
	}

	// Bot changes the note of Ada: Bot becomes the author; the stub stays as it is.
	change := &notev1.ChangeNoteRequest{CommandId: coreapi.NewCommandID(), Id: n1.GetId(), SessionId: m.sid, Text: "Call the bank on Monday.",
		AuthorParticipantId: bot, Tags: []string{"bank"}}
	res, err := m.a(bot).Note.ChangeNote(m.ctx(), change)
	if err != nil {
		t.Fatal(err)
	}
	if n := res.GetNote(); res.GetDuplicate() || n.GetId() != 1 || n.GetText() != "Call the bank on Monday." || n.GetAuthorParticipantId() != bot ||
		n.GetAuthorKind() != notev1.AuthorKind_AUTHOR_KIND_AGENT || !n.GetStaged() || n.GetCommandId() != n1.GetCommandId() || len(n.GetTags()) != 1 {
		t.Fatalf("changed note: %v", res)
	}
	if _, body := frontMatter(t, "notes/000001.md", stagedFile(t, m.srv, "notes/000001.md")); body != "Call the bank on Monday.\n" {
		t.Errorf("the staged file was not changed: %q", body)
	}
	if c := commitsAt(t, m.srv, "notes/000001.md"); len(c) != 1 {
		t.Errorf("a change of a staged note touched its stub: %v", c)
	}
	again, err := m.a(bot).Note.ChangeNote(m.ctx(), change)
	if err != nil || !again.GetDuplicate() || again.GetNote().GetText() != "Call the bank on Monday." {
		t.Errorf("repeated change: %v, %v", again, err)
	}
	reused := &notev1.ChangeNoteRequest{CommandId: change.GetCommandId(), Id: 1, SessionId: m.sid, Text: "Other", AuthorParticipantId: bot}
	_, err = m.a(bot).Note.ChangeNote(m.ctx(), reused)
	wantCode(t, "a change with a reused command_id", err, connect.CodeAlreadyExists)

	refused := func(what string, a string, id uint32, session string, code connect.Code) {
		t.Helper()
		_, err := m.a(a).Note.ChangeNote(m.ctx(), &notev1.ChangeNoteRequest{CommandId: coreapi.NewCommandID(), Id: id, SessionId: session,
			Text: "x", AuthorParticipantId: a})
		wantCode(t, "change "+what, err, code)
		_, err = m.a(a).Note.RemoveNote(m.ctx(), &notev1.RemoveNoteRequest{CommandId: coreapi.NewCommandID(), Id: id, SessionId: session,
			AuthorParticipantId: a})
		wantCode(t, "remove "+what, err, code)
	}
	refused("naming no session", ada, 1, "", connect.CodeFailedPrecondition)
	refused("naming another session", ada, 1, other, connect.CodeFailedPrecondition)
	refused("by a member who did not take part", carl, 1, m.sid, connect.CodePermissionDenied)
	refused("of a released note", ada, free.GetId(), "", connect.CodeFailedPrecondition)
	refused("of a note that does not exist", ada, 99, m.sid, connect.CodeNotFound)

	// Ada removes the decision of Bot. Readers no longer see it; others still see the stub.
	rm := &decisionv1.RemoveDecisionRequest{CommandId: coreapi.NewCommandID(), Id: d1.GetId(), SessionId: m.sid, AuthorParticipantId: ada}
	if r, err := m.a(ada).Decision.RemoveDecision(m.ctx(), rm); err != nil || r.GetDuplicate() {
		t.Fatalf("remove the decision: %v, %v", r, err)
	}
	if r, err := m.a(ada).Decision.RemoveDecision(m.ctx(), rm); err != nil || !r.GetDuplicate() {
		t.Errorf("repeated removal: %v, %v", r, err)
	}
	if ds := listDecisions(t, m.a(bot), &decisionv1.ListDecisionsRequest{}); len(ds) != 0 {
		t.Errorf("a removed decision is listed for a reader: %v", ds)
	}
	if ds := listDecisions(t, m.a(carl), &decisionv1.ListDecisionsRequest{}); len(ds) != 1 || !isStub(1, ds[0]) {
		t.Errorf("decisions for a member who did not take part: %v", ds)
	}
	_, err = m.a(ada).Decision.ChangeDecision(m.ctx(), &decisionv1.ChangeDecisionRequest{CommandId: coreapi.NewCommandID(), Id: 1,
		SessionId: m.sid, Text: "x", AuthorParticipantId: ada})
	wantCode(t, "change a removed decision", err, connect.CodeNotFound)

	// The minutes are staged at the end; a participant changes them before the release.
	if _, err := m.a(bot).Commands.UpdateMinutes(m.ctx(), &sessioncommandsv1.UpdateMinutesRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: bot, Text: "# Minutes\n\nDraft"}); err != nil {
		t.Fatal(err)
	}
	m.end(ada)
	mres, err := m.a(ada).Minutes.GetMinutes(m.ctx(), &minutesv1.GetMinutesRequest{SessionId: m.sid})
	if err != nil || !mres.GetMinutes().GetStaged() {
		t.Fatalf("staged minutes: %v, %v", mres, err)
	}
	if got := m.artifact("minutes/000001.yaml"); got != string(recordscheck.Stub(recordscheck.SchemaMinutes)) {
		t.Errorf("minutes/000001.yaml in the artifact repository is not the stub:\n%s", got)
	}
	cm, err := m.a(ada).Minutes.ChangeMinutes(m.ctx(), &minutesv1.ChangeMinutesRequest{CommandId: coreapi.NewCommandID(),
		Id: mres.GetMinutes().GetId(), SessionId: m.sid, Text: "# Minutes\n\nFinal", AuthorParticipantId: ada})
	if err != nil || cm.GetMinutes().GetText() != "# Minutes\n\nFinal" || cm.GetMinutes().GetAuthorParticipantId() != ada {
		t.Fatalf("change the minutes after the end: %v, %v", cm, err)
	}

	// The release moves the records; their stubs are replaced, and the stub of the removed
	// decision is gone.
	if _, err := m.releaseBy(ada); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 15*time.Second, "the released note for every member", func() bool {
		ns := notesOf(t, m.a(carl), m.sid)
		return len(ns) == 2 && !ns[0].GetStaged() && ns[0].GetText() == "Call the bank on Monday."
	})
	if _, body := frontMatter(t, "notes/000001.md", m.artifact("notes/000001.md")); body != "Call the bank on Monday.\n" {
		t.Errorf("the released note: %q", body)
	}
	if got := m.artifact("decisions/000001.yaml"); got != "" {
		t.Errorf("the stub of the removed decision is still there:\n%s", got)
	}
	if !strings.Contains(m.artifact("minutes/000001.yaml"), "Final") {
		t.Errorf("the released minutes:\n%s", m.artifact("minutes/000001.yaml"))
	}
	waitUntil(t, 15*time.Second, "the released minutes for every member", func() bool {
		r, err := m.a(carl).Minutes.GetMinutes(m.ctx(), &minutesv1.GetMinutesRequest{SessionId: m.sid})
		return err == nil && !r.GetMinutes().GetStaged() && r.GetMinutes().GetText() == "# Minutes\n\nFinal"
	})
	if ds := listDecisions(t, m.a(carl), &decisionv1.ListDecisionsRequest{}); len(ds) != 0 {
		t.Errorf("decisions after the release: %v", ds)
	}

	// A released record never changes.
	refused("after the release", ada, 1, m.sid, connect.CodeFailedPrecondition)
	// A repeated command returns the record from the artifact repository now.
	dup, err := m.a(ada).Note.AddNote(m.ctx(), keepReq)
	if err != nil || !dup.GetDuplicate() || dup.GetNote().GetId() != kept.GetNote().GetId() || dup.GetNote().GetStaged() {
		t.Errorf("repeated note after the release: %v, %v", dup, err)
	}
	if again, err := m.a(bot).Note.ChangeNote(m.ctx(), change); err != nil || !again.GetDuplicate() || again.GetNote().GetStaged() {
		t.Errorf("repeated change after the release: %v, %v", again, err)
	}
	// A record that names the released session is released at once.
	late := m.note(bot, m.sid, "Written after the release.")
	if late.GetStaged() || late.GetId() != 4 {
		t.Errorf("a note of a released session: %v", late)
	}
	if _, body := frontMatter(t, "notes/000004.md", m.artifact("notes/000004.md")); body != "Written after the release.\n" {
		t.Errorf("the note of a released session in the artifact repository: %q", body)
	}
}

// TestStagedRecordsSurviveARestart: the hub reads the staged records back from the staging
// repository when it starts, with their readers and their stubs.
func TestStagedRecordsSurviveARestart(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	ada := m.join("Ada", kHuman)
	carl := m.register("Carl", kHuman).ParticipantID
	req := &notev1.AddNoteRequest{CommandId: coreapi.NewCommandID(), Text: "Staged before the restart.", AuthorParticipantId: ada, SessionId: m.sid}
	first, err := m.a(ada).Note.AddNote(m.ctx(), req)
	if err != nil {
		t.Fatal(err)
	}
	m.restart(false, nil, "-release-grace-period", "1h")
	if got := notesOf(t, m.a(ada), ""); len(got) != 1 || got[0].String() != first.GetNote().String() {
		t.Errorf("notes of a reader after the restart: %v, want %v", got, first.GetNote())
	}
	if got := notesOf(t, m.a(carl), ""); len(got) != 1 || !isStub(1, got[0]) {
		t.Errorf("notes of a member who did not take part after the restart: %v", got)
	}
	again, err := m.a(ada).Note.AddNote(m.ctx(), req)
	if err != nil || !again.GetDuplicate() || again.GetNote().String() != first.GetNote().String() {
		t.Errorf("repeated note after the restart: %v, %v", again, err)
	}
	if n := m.note(ada, m.sid, "After the restart."); n.GetId() != 2 || !n.GetStaged() {
		t.Errorf("the next staged note: %v", n)
	}
}

// TestStagedRecordsWaitForTheMove: while a release moves the area of a session, the release
// status (released) and the staging area disagree. A write that names the session is refused,
// and the client repeats it later.
func TestStagedRecordsWaitForTheMove(t *testing.T) {
	gate := contracttest.NewArtifactGate(t, contracttest.DefaultTenant)
	m := &meeting{t: t, srv: contracttest.StartHub(t, gate.Env, "-release-grace-period", "1h"), sid: "s-" + coreapi.NewCommandID(),
		actors: map[string]*contracttest.Actor{}}
	m.reader = m.srv.Participant(t, "reader", "Reader", agent)
	ada := m.join("Ada", kHuman)
	n := m.note(ada, m.sid, "Staged.")
	m.end(ada)

	gate.Close(t)
	if _, err := m.releaseBy(ada); err != nil {
		t.Fatalf("release: %v", err)
	}
	_, err := m.a(ada).Note.AddNote(m.ctx(), &notev1.AddNoteRequest{CommandId: coreapi.NewCommandID(), Text: "During the move.",
		AuthorParticipantId: ada, SessionId: m.sid})
	wantCode(t, "a new note while the area moves", err, connect.CodeUnavailable)
	_, err = m.a(ada).Note.ChangeNote(m.ctx(), &notev1.ChangeNoteRequest{CommandId: coreapi.NewCommandID(), Id: n.GetId(), SessionId: m.sid,
		Text: "Changed during the move.", AuthorParticipantId: ada})
	wantCode(t, "a change while the area moves", err, connect.CodeUnavailable)
	gate.Open(t)
}
