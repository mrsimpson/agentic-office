package contract

// Contract tests of the release of a session (concept-session-release) through
// if-session-commands and if-session-stream, against the real office-hub. The documents part does
// not write the staging area yet (#202), so the tests stage documents with
// contracttest.StageFile, as a part of the hub would. The records stage themselves
// (staged_records_test.go).

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionimportv1 "github.com/mrsimpson/agentic-office/hub/session/importapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const (
	kHuman = sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN
	kAgent = sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT
)

func (m *meeting) staging() string { return m.srv.TenantStagingDir(contracttest.DefaultTenant) }

func (m *meeting) endWithStatus(pid string) *sessioncommandsv1.EndSessionResponse {
	m.t.Helper()
	res, err := m.s(pid).EndSession(m.ctx(), &sessioncommandsv1.EndSessionRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: pid})
	if err != nil {
		m.t.Fatalf("end: %v", err)
	}
	return res
}

func (m *meeting) releaseBy(pid string) (*sessioncommandsv1.ReleaseSessionResponse, error) {
	return m.s(pid).ReleaseSession(m.ctx(), &sessioncommandsv1.ReleaseSessionRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: pid})
}

func (m *meeting) objectBy(pid string) (*sessioncommandsv1.ObjectToReleaseResponse, error) {
	return m.s(pid).ObjectToRelease(m.ctx(), &sessioncommandsv1.ObjectToReleaseRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: pid})
}

func (m *meeting) markOff(pid string, id uint32) error {
	_, err := m.s(pid).MarkOffTheRecord(m.ctx(), &sessioncommandsv1.MarkOffTheRecordRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: pid, UtteranceEventId: id})
	return err
}

// events returns the whole log of the session as a participant reads it.
func (m *meeting) logOf(pid string) []*sessionstreamv1.Event {
	m.t.Helper()
	res, err := m.stream(pid).GetEvents(m.ctx(), &sessionstreamv1.GetEventsRequest{SessionId: m.sid, Limit: 1000})
	if err != nil {
		m.t.Fatalf("events: %v", err)
	}
	return res.GetEvents()
}

// released returns the session.released event of the log, or nil.
func (m *meeting) released(pid string) *sessionstreamv1.Event {
	m.t.Helper()
	for _, ev := range m.logOf(pid) {
		if ev.GetType() == events.SessionReleased {
			return ev
		}
	}
	return nil
}

// artifact returns the content of a file in the artifact repository of the tenant, or "".
func (m *meeting) artifact(path string) string {
	m.t.Helper()
	for _, f := range contracttest.Files(m.t, m.srv.ArchiveDir()) {
		if f == path {
			return contracttest.Git(m.t, m.srv.ArchiveDir(), "show", "main:"+path)
		}
	}
	return ""
}

// restart restarts the hub on the same address and data directory: gracefully, or killed.
func (m *meeting) restart(kill bool, env []string, args ...string) {
	m.t.Helper()
	if kill {
		m.srv.Kill()
	} else if _, err := m.srv.Stop(10 * time.Second); err != nil {
		m.t.Fatal(err)
	}
	m.start(env, args...)
}

// start starts the stopped hub again on the same address and data directory. The tokens of the
// actors are gone with the old process, so every actor signs in again.
func (m *meeting) start(env []string, args ...string) {
	m.t.Helper()
	m.srv = contracttest.StartHubIn(m.t, m.srv.DataDir, m.srv.AdminKey, env, append([]string{"-addr", m.srv.Addr()}, args...)...)
	for pid, a := range m.actors {
		m.actors[pid] = m.srv.WithKey(m.t, a.Key)
	}
}

// The staging area of a session opens with the session, and every participant who joins becomes
// a reader. Before the end the session is open and takes no release or objection.
func TestReleaseAreaOpensWithTheSession(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	ada := m.join("Ada", kHuman)
	bot := m.join("Bot", kAgent)
	area := contracttest.AreaState(t, m.staging(), m.sid)
	if area == nil {
		t.Fatal("the session has no staging area")
	}
	readers, _ := area["readers"].([]any)
	if len(readers) != 2 || readers[0] != ada || readers[1] != bot {
		t.Fatalf("readers %v, want [%s %s]", readers, ada, bot)
	}
	if _, ok := area["deadline"]; ok {
		t.Fatalf("a running session has a deadline: %v", area)
	}
	_, err := m.releaseBy(ada)
	wantCode(t, "release before the end", err, connect.CodeFailedPrecondition)
	_, err = m.objectBy(ada)
	wantCode(t, "objection before the end", err, connect.CodeFailedPrecondition)
}

// A human who took part releases the session: the release is an event in the log, and the staged
// files move into the artifact repository in one commit with the human as the author. Agents and
// people who did not take part are refused. The answer of the release names the places where a
// staged file quotes a sentence off the record word for word, and the hub changes no content.
func TestReleaseByAHuman(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	ada := m.join("Ada", kHuman)
	bot := m.join("Bot", kAgent)
	carl := m.register("Carl", kHuman).ParticipantID // a member who never joined
	m.say(ada, "We meet on Monday.")
	secret := m.say(ada, "Ben will leave the company in March. That is still open.")
	if err := m.markOff(ada, secret.GetId()); err != nil {
		t.Fatal(err)
	}
	notes := "# Notes\n\nPlan:\nben will leave the\n  company in March\n"
	contracttest.StageFile(t, m.staging(), m.sid, "documents/notes.md", notes)
	contracttest.StageFile(t, m.staging(), m.sid, "documents/plan.md", "# Plan\n\nWe meet on Monday.\n")

	end := m.endWithStatus(ada)
	if st := end.GetRelease().GetState(); st != sessioncommandsv1.ReleaseState_RELEASE_STATE_WAITING {
		t.Fatalf("release state after the end: %v", st)
	}
	if d := end.GetRelease().GetDeadline().AsTime(); d.Before(time.Now().Add(50*time.Minute)) || d.After(time.Now().Add(61*time.Minute)) {
		t.Errorf("deadline %v, want the end plus the grace period of one hour", d)
	}
	if area := contracttest.AreaState(t, m.staging(), m.sid); area == nil || area["deadline"] == nil {
		t.Errorf("the staging area has no deadline: %v", area)
	}

	_, err := m.releaseBy(bot)
	wantCode(t, "release by an agent", err, connect.CodePermissionDenied)
	_, err = m.objectBy(bot)
	wantCode(t, "objection by an agent", err, connect.CodePermissionDenied)
	_, err = m.releaseBy(carl)
	wantCode(t, "release by a human who did not take part", err, connect.CodeNotFound)
	_, err = m.objectBy(carl)
	wantCode(t, "objection by a human who did not take part", err, connect.CodeNotFound)
	if m.released(ada) != nil || m.artifact("documents/notes.md") != "" {
		t.Fatal("a refused release released the session")
	}

	rel, err := m.releaseBy(ada)
	if err != nil {
		t.Fatal(err)
	}
	ev := rel.GetResult().GetEvent()
	if ev.GetType() != events.SessionReleased || ev.GetActorParticipantId() != ada {
		t.Fatalf("release event %v", ev)
	}
	want := []*sessioncommandsv1.Quote{{Path: "documents/notes.md", Line: 4, UtteranceEventId: secret.GetId()}}
	if got := rel.GetQuotes(); len(got) != len(want) || got[0].GetPath() != want[0].Path ||
		got[0].GetLine() != want[0].Line || got[0].GetUtteranceEventId() != want[0].UtteranceEventId {
		t.Fatalf("quotes %v, want %v", got, want)
	}
	if got := m.artifact("documents/notes.md"); got != notes {
		t.Errorf("released note %q, want it unchanged %q", got, notes)
	}
	if m.artifact("documents/plan.md") == "" {
		t.Error("the document was not released")
	}
	if got := contracttest.ReleaseCommits(t, m.srv.ArchiveDir(), m.sid); len(got) != 1 || got[0] != "Ada <"+ada+"@office.invalid>" {
		t.Errorf("release commits %v, want one by Ada", got)
	}
	if contracttest.AreaState(t, m.staging(), m.sid) != nil {
		t.Error("the staging area is still there after the release")
	}
	// The log keeps one release; a second fails, and so does an objection.
	_, err = m.releaseBy(ada)
	wantCode(t, "second release", err, connect.CodeFailedPrecondition)
	_, err = m.objectBy(ada)
	wantCode(t, "objection after the release", err, connect.CodeFailedPrecondition)
	// A repeated command returns the stored event.
	again, err := m.s(ada).ReleaseSession(m.ctx(), &sessioncommandsv1.ReleaseSessionRequest{SessionId: m.sid,
		CommandId: ev.GetCommandId(), ParticipantId: ada})
	if err != nil || !again.GetResult().GetDuplicate() || again.GetResult().GetEvent().GetId() != ev.GetId() {
		t.Errorf("repeated release: %v, %v", again, err)
	}
}

// An objection holds the session past its deadline, until a human releases it. A human who left
// before the end may still object and release.
func TestReleaseObjectionHolds(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1s")
	ada := m.join("Ada", kHuman)
	ben := m.join("Ben", kHuman)
	contracttest.StageFile(t, m.staging(), m.sid, "documents/draft.md", "# Draft\n")
	if _, err := m.s(ben).Leave(m.ctx(), &sessioncommandsv1.LeaveRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(), ParticipantId: ben}); err != nil {
		t.Fatal(err)
	}
	m.end(ada)
	obj, err := m.objectBy(ben)
	if err != nil {
		t.Fatal(err)
	}
	if ev := obj.GetResult().GetEvent(); ev.GetType() != events.ReleaseObjected || ev.GetActorParticipantId() != ben {
		t.Fatalf("objection event %v", ev)
	}
	if area := contracttest.AreaState(t, m.staging(), m.sid); area == nil || area["held"] != true {
		t.Errorf("the staging area does not record the objection: %v", area)
	}
	_, err = m.objectBy(ada)
	wantCode(t, "second objection", err, connect.CodeFailedPrecondition)
	// The deadline passes; the session stays held.
	time.Sleep(2500 * time.Millisecond)
	if m.released(ada) != nil || m.artifact("documents/draft.md") != "" {
		t.Fatal("a held session was released at its deadline")
	}
	if _, err := m.releaseBy(ben); err != nil {
		t.Fatal(err)
	}
	if m.artifact("documents/draft.md") == "" {
		t.Error("the release did not move the staged file")
	}
}

// When the grace period ends, the hub releases the session with itself as the author.
func TestReleaseAfterTheGracePeriod(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "2s")
	ada := m.join("Ada", kHuman)
	contracttest.StageFile(t, m.staging(), m.sid, "documents/later.md", "# Later\n")
	m.end(ada)
	if m.released(ada) != nil {
		t.Fatal("released before the deadline")
	}
	waitUntil(t, 15*time.Second, "the release at the deadline", func() bool { return m.artifact("documents/later.md") != "" })
	ev := m.released(ada)
	if ev == nil || ev.GetActorParticipantId() != "" {
		t.Fatalf("release event %v, want one by the hub", ev)
	}
	if got := contracttest.ReleaseCommits(t, m.srv.ArchiveDir(), m.sid); len(got) != 1 || strings.Contains(got[0], ada) {
		t.Errorf("release commits %v, want one by the hub", got)
	}
}

// A session without humans is released at its end: a meeting of agents, and an import that only
// agents worked on, even if the imported speakers were humans.
func TestReleaseWithoutHumans(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	bot := m.join("Bot", kAgent)
	contracttest.StageFile(t, m.staging(), m.sid, "documents/bots.md", "# Bots\n")
	if st := m.endWithStatus(bot).GetRelease().GetState(); st != sessioncommandsv1.ReleaseState_RELEASE_STATE_RELEASED {
		t.Fatalf("release state at the end of a session of agents: %v", st)
	}
	if m.artifact("documents/bots.md") == "" {
		t.Error("the staged file was not released")
	}
	if ev := m.released(bot); ev == nil || ev.GetActorParticipantId() != "" {
		t.Errorf("release event %v, want one by the hub", ev)
	}

	im := newImporter(t, m.srv, "porter")
	im.ok(&sessionimportv1.ImportConversationRequest{Conversation: conversation(), Participants: people(),
		Utterances: []*sessionimportv1.ImportedUtterance{utt("ada", "Hello.")}, Complete: true})
	if area := contracttest.AreaState(t, m.staging(), im.sid); area == nil {
		t.Fatal("the import has no staging area")
	} else if r, _ := area["readers"].([]any); len(r) != 1 || r[0] != "porter" {
		t.Errorf("readers of the import %v, want the importer", r)
	}
	contracttest.StageFile(t, m.staging(), im.sid, "documents/imported.md", "# Imported\n")
	if _, err := im.a.Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: im.sid, CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatal(err)
	}
	res, err := im.a.Commands.EndSession(ctx10(t), &sessioncommandsv1.EndSessionRequest{SessionId: im.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: "porter"})
	if err != nil {
		t.Fatal(err)
	}
	if st := res.GetRelease().GetState(); st != sessioncommandsv1.ReleaseState_RELEASE_STATE_RELEASED {
		t.Fatalf("release state at the end of an import by an agent: %v", st)
	}
	if m.artifact("documents/imported.md") == "" {
		t.Error("the staged file of the import was not released")
	}
}

// The deadlines survive a restart: a session whose grace period runs during the restart is
// released at its deadline, and a session whose deadline passed while the hub was down is
// released at start.
func TestReleaseDeadlinesSurviveARestart(t *testing.T) {
	args := []string{"-release-grace-period", "3s"}
	m := newMeetingArgs(t, args...)
	ada := m.join("Ada", kHuman)
	contracttest.StageFile(t, m.staging(), m.sid, "documents/during.md", "# During\n")
	m.end(ada)
	m.restart(false, nil, args...)
	if m.artifact("documents/during.md") != "" {
		t.Fatal("released before the deadline")
	}
	waitUntil(t, 15*time.Second, "the release after the restart", func() bool { return m.artifact("documents/during.md") != "" })
	if ev := m.released(ada); ev == nil || ev.GetActorParticipantId() != "" {
		t.Fatalf("release event %v, want one by the hub", ev)
	}

	// The second session ends; the hub is down when its deadline passes.
	first := m.sid
	m.sid = "s-" + coreapi.NewCommandID()
	if _, err := m.s(ada).Join(m.ctx(), &sessioncommandsv1.JoinRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatal(err)
	}
	contracttest.StageFile(t, m.staging(), m.sid, "documents/down.md", "# Down\n")
	m.end(ada)
	if _, err := m.srv.Stop(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second)
	m.start(nil, args...)
	waitUntil(t, 5*time.Second, "the release at start", func() bool { return m.artifact("documents/down.md") != "" })
	if got := contracttest.ReleaseCommits(t, m.srv.ArchiveDir(), first); len(got) != 1 {
		t.Errorf("the first session has %d release commits, want 1", len(got))
	}
}

// A restart cuts a release off after the hub closed the staging area: the release is stored in
// the log before the hub answers, and the hub completes it at start, once, with the human who
// released as the author.
func TestReleaseCutOffByARestartCompletes(t *testing.T) {
	gate := contracttest.NewArtifactGate(t, contracttest.DefaultTenant)
	args := []string{"-release-grace-period", "1h"}
	m := &meeting{t: t, srv: contracttest.StartHub(t, gate.Env, args...), sid: "s-" + coreapi.NewCommandID(), actors: map[string]*contracttest.Actor{}}
	m.reader = m.srv.Participant(t, "reader", "Reader", agent)
	ada := m.join("Ada", kHuman)
	contracttest.StageFile(t, m.staging(), m.sid, "documents/cut.md", "# Cut\n")
	m.end(ada)

	gate.Close(t)
	if _, err := m.releaseBy(ada); err != nil {
		t.Fatalf("release: %v", err)
	}
	area := contracttest.AreaState(t, m.staging(), m.sid)
	if area == nil || area["release"] == nil {
		t.Fatalf("the staging area is not closed for its release: %v", area)
	}
	if m.artifact("documents/cut.md") != "" {
		t.Fatal("the gate did not stop the release")
	}

	m.restart(true, gate.Env, args...)
	gate.Open(t)
	// The worker tries again a few seconds after a failure; the restart started one at once, so
	// wait for either.
	waitUntil(t, 15*time.Second, "the release after the restart", func() bool { return m.artifact("documents/cut.md") != "" })
	if got := contracttest.ReleaseCommits(t, m.srv.ArchiveDir(), m.sid); len(got) != 1 || got[0] != "Ada <"+ada+"@office.invalid>" {
		t.Errorf("release commits %v, want one by Ada", got)
	}
	waitUntil(t, 5*time.Second, "the clean-up of the staging area", func() bool { return contracttest.AreaState(t, m.staging(), m.sid) == nil })
	if ev := m.released(ada); ev == nil || ev.GetActorParticipantId() != ada {
		t.Errorf("release event %v, want one by Ada", ev)
	}
}

// After the end, the session takes exactly three kinds of events: the release commands, notices
// about artifacts and, until the release, marks off the record. The hub stores each before it
// answers, so a crash right after loses nothing. The stream of the session still ends with
// session.ended.
func TestReleaseEventsAfterTheEnd(t *testing.T) {
	args := []string{"-release-grace-period", "1h", "-archive-interval", "1h"}
	m := newMeetingArgs(t, args...)
	ada := m.join("Ada", kHuman)
	said := m.say(ada, "This goes off the record later.")
	ended := m.end(ada)

	_, err := m.s(ada).SubmitUtterance(m.ctx(), m.utterance(ada, "Too late."))
	wantCode(t, "utterance after the end", err, connect.CodeFailedPrecondition)
	if err := m.markOff(ada, said.GetId()); err != nil {
		t.Fatalf("mark off the record after the end: %v", err)
	}
	_, err = m.actors[ada].Document.CreateDocument(m.ctx(), &documentv1.CreateDocumentRequest{Path: "documents/after.md",
		Content: "# After\n", CommandId: coreapi.NewCommandID(), SessionId: m.sid})
	if err != nil {
		t.Fatalf("document for the ended session: %v", err)
	}

	// The hub dies without a last write; the events are in the archive.
	m.restart(true, nil, args...)
	evs := m.logOf(ada)
	var types []string
	for _, ev := range evs[ended.GetId():] {
		types = append(types, ev.GetType())
	}
	if want := []string{events.UtteranceOffTheRecord, events.ArtifactCreated}; !slices.Equal(types, want) {
		t.Fatalf("events after the end %v, want %v", types, want)
	}
	// A subscriber from the start gets the log up to session.ended, with the tree head over it, and
	// then the end of the stream.
	st, err := m.stream(ada).Subscribe(m.ctx(), &sessionstreamv1.SubscribeRequest{SessionId: m.sid})
	if err != nil {
		t.Fatal(err)
	}
	var last *sessionstreamv1.SubscribeResponse
	for {
		res, err := st.Receive()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		last = res
	}
	st.Close()
	if last.GetEvent().GetId() != ended.GetId() || last.GetTreeHead().GetTreeSize() != ended.GetId() {
		t.Fatalf("the stream ended with %v, want session.ended with its tree head", last)
	}

	if _, err := m.releaseBy(ada); err != nil {
		t.Fatal(err)
	}
	err = m.markOff(ada, said.GetId())
	wantCode(t, "mark off the record after the release", err, connect.CodeFailedPrecondition)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = m.actors[ada].Document.CreateDocument(ctx, &documentv1.CreateDocumentRequest{Path: "documents/released.md",
		Content: "# Released\n", CommandId: coreapi.NewCommandID(), SessionId: m.sid})
	if err != nil {
		t.Fatalf("document for the released session: %v", err)
	}
}
