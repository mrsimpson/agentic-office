package contract

// Contract tests of the session import (if-session-import, dec-session-import), realized by the
// meeting session. They start the real office-hub and use only the Connect clients of the
// session import, the session commands and stream, the records and the archive on disk.

import (
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/hub/session/api/events"
	"github.com/mrsimpson/agentic-office/hub/session/api/merkle"
	sessionimportv1 "github.com/mrsimpson/agentic-office/hub/session/importapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const (
	pHuman = sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN
	pAgent = sessionv1.ParticipantKind_PARTICIPANT_KIND_AGENT
	mText  = sessionv1.Modality_MODALITY_TEXT
	mVoice = sessionv1.Modality_MODALITY_VOICE
)

// importer is a participant who imports into one session.
type importer struct {
	t   *testing.T
	a   *contracttest.Actor
	sid string
}

func newImporter(t *testing.T, srv *contracttest.Hub, pid string) *importer {
	t.Helper()
	return &importer{t: t, a: srv.Participant(t, pid, pid, agent), sid: "imp-" + ids.NewCommandID()}
}

// page sends one page with a new command ID. The caller sets everything but the IDs.
func (im *importer) page(req *sessionimportv1.ImportConversationRequest) (*sessionimportv1.ImportConversationResponse, error) {
	if req.SessionId == "" {
		req.SessionId = im.sid
	}
	if req.CommandId == "" {
		req.CommandId = ids.NewCommandID()
	}
	if req.ParticipantId == "" {
		req.ParticipantId = im.a.ParticipantID
	}
	return im.a.Import.ImportConversation(ctx10(im.t), req)
}

func (im *importer) ok(req *sessionimportv1.ImportConversationRequest) *sessionimportv1.ImportConversationResponse {
	im.t.Helper()
	res, err := im.page(req)
	if err != nil {
		im.t.Fatalf("import page: %v", err)
	}
	return res
}

func (im *importer) fails(code connect.Code, req *sessionimportv1.ImportConversationRequest) {
	im.t.Helper()
	if _, err := im.page(req); connect.CodeOf(err) != code {
		im.t.Fatalf("import page: want %v, got %v", code, err)
	}
}

func (im *importer) events() []*sessionv1.Event {
	im.t.Helper()
	res, err := im.a.Session.GetEvents(ctx10(im.t), &sessionv1.GetEventsRequest{SessionId: im.sid, Limit: 1000})
	if err != nil {
		im.t.Fatal(err)
	}
	return res.GetEvents()
}

func conversation() *sessionimportv1.Conversation {
	return &sessionimportv1.Conversation{Source: "Chat export, #design", Title: "Design review"}
}

func people() []*sessionimportv1.ImportedParticipant {
	return []*sessionimportv1.ImportedParticipant{
		{Key: "ada", DisplayName: "Ada", Kind: pHuman},
		{Key: "claude", DisplayName: "Claude", Kind: pAgent},
	}
}

func utt(speaker, text string) *sessionimportv1.ImportedUtterance {
	return &sessionimportv1.ImportedUtterance{Speaker: speaker, Text: text, Modality: mText}
}

// TestImportInPages: a conversation in three pages becomes a marked session. Its first event is
// session.imported with the source, the speakers join as imported participants, the utterances
// keep their order and time, and import.completed ends the import. Every event carries the
// command ID of its page and the importer as actor. The stream, the events after a cursor and
// the archive deliver the same log, and the signed tree heads cover it. A repeated page stores
// nothing new.
func TestImportInPages(t *testing.T) {
	srv := contracttest.StartHub(t, nil, "-archive-interval", "1h")
	im := newImporter(t, srv, "importer")
	spoken := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

	p1 := &sessionimportv1.ImportConversationRequest{CommandId: ids.NewCommandID(), Conversation: conversation(),
		Participants: people(), Utterances: []*sessionimportv1.ImportedUtterance{
			{Speaker: "ada", Text: "Shall we ship the import?", Modality: mVoice, Time: timestamppb.New(spoken)},
			utt("claude", "Yes, with contract tests."),
		}}
	r1 := im.ok(p1)
	// session.imported, two participants, two utterances.
	if r1.GetFirstEventId() != 1 || r1.GetLastEventId() != 5 || r1.GetDuplicate() || r1.GetComplete() {
		t.Fatalf("first page: %v", r1)
	}
	again, err := im.page(p1)
	if err != nil || !again.GetDuplicate() || again.GetFirstEventId() != 1 || again.GetLastEventId() != 5 {
		t.Fatalf("repeated first page: %v, %v", again, err)
	}
	r2 := im.ok(&sessionimportv1.ImportConversationRequest{
		Participants: []*sessionimportv1.ImportedParticipant{{Key: "bo", DisplayName: "Bo", Kind: pHuman}},
		Utterances:   []*sessionimportv1.ImportedUtterance{utt("bo", "Agreed."), utt("ada", "Then it is decided.")}})
	if r2.GetFirstEventId() != 6 || r2.GetLastEventId() != 8 {
		t.Fatalf("second page: %v", r2)
	}
	r3 := im.ok(&sessionimportv1.ImportConversationRequest{Complete: true})
	if r3.GetFirstEventId() != 9 || r3.GetLastEventId() != 9 || !r3.GetComplete() {
		t.Fatalf("last page: %v", r3)
	}

	evs := im.events()
	wantTypes := []string{events.SessionImported, events.ParticipantJoined, events.ParticipantJoined, events.UtteranceFinal,
		events.UtteranceFinal, events.ParticipantJoined, events.UtteranceFinal, events.UtteranceFinal, events.ImportCompleted}
	if len(evs) != len(wantTypes) {
		t.Fatalf("%d events, want %d: %v", len(evs), len(wantTypes), evs)
	}
	for i, ev := range evs {
		if ev.GetType() != wantTypes[i] || ev.GetId() != uint32(i+1) || ev.GetActorParticipantId() != "importer" {
			t.Errorf("event %d: %v", i+1, ev)
		}
	}
	if si := evs[0].GetSessionImported(); si.GetSource() != "Chat export, #design" || si.GetTitle() != "Design review" ||
		evs[0].GetCommandId() != p1.GetCommandId() {
		t.Errorf("session.imported: %v", evs[0])
	}
	if j := evs[1].GetParticipantJoined(); j.GetParticipantId() != "imported:ada" || j.GetDisplayName() != "Ada" ||
		j.GetKind() != pHuman || !j.GetImported() || j.GetCaptured() {
		t.Errorf("imported participant: %v", evs[1])
	}
	if j := evs[2].GetParticipantJoined(); j.GetParticipantId() != "imported:claude" || j.GetKind() != pAgent {
		t.Errorf("imported agent: %v", evs[2])
	}
	u := evs[3].GetUtteranceFinal()
	if u.GetParticipantId() != "imported:ada" || u.GetText() != "Shall we ship the import?" || u.GetModality() != mVoice ||
		!u.GetOriginalTime().AsTime().Equal(spoken) || u.GetTranscribedAt() != nil {
		t.Errorf("imported utterance: %v", evs[3])
	}
	if evs[4].GetUtteranceFinal().GetOriginalTime() != nil {
		t.Errorf("an utterance without time got one: %v", evs[4])
	}
	if c := evs[8].GetImportCompleted(); c.GetUtterances() != 4 {
		t.Errorf("import.completed: %v", evs[8])
	}

	// Signed tree heads: the one of the last page covers the whole log.
	key := hubKey(t, im.a.Session)
	if err := merkle.VerifyEvents(evs, r3.GetTreeHead(), key); err != nil {
		t.Fatalf("tree head of the last page: %v", err)
	}
	if err := merkle.VerifyEvents(evs[:5], again.GetTreeHead(), key); err != nil {
		t.Fatalf("tree head of the repeated first page: %v", err)
	}
	// The stream delivers the same log.
	c := follow(t, im.a, im.sid, len(evs))
	if err := merkle.VerifyEvents(c.events, c.head, key); err != nil || c.head.GetTreeSize() != 9 {
		t.Fatalf("stream: %v (head %v)", err, c.head)
	}
	// The end of the import starts a write to the archive, long before the interval.
	a := contracttest.WaitArchived(t, srv.ArchiveDir(), im.sid, 10*time.Second, func(a *contracttest.ArchivedSession) bool {
		return len(a.Events) == len(evs)
	})
	a.Verify(t, im.sid, key)
	if a.Events[0].GetSessionImported().GetSource() != "Chat export, #design" {
		t.Errorf("archived session is not marked as imported: %v", a.Events[0])
	}
}

// TestImportThenDeriveRecords: after the import, the session takes session commands like any
// other. A participant joins, writes the minutes and ends the session; the hub stores the minutes.
// Decisions refer to the imported session.
func TestImportThenDeriveRecords(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	im := newImporter(t, srv, "scribe")
	im.ok(&sessionimportv1.ImportConversationRequest{Conversation: conversation(), Participants: people(),
		Utterances: []*sessionimportv1.ImportedUtterance{utt("ada", "We ship on Friday."), utt("claude", "I will write the notes.")},
		Complete:   true})
	ctx := ctx10(t)
	s := im.a.Session
	if _, err := s.Join(ctx, &sessionv1.JoinRequest{SessionId: im.sid, CommandId: ids.NewCommandID()}); err != nil {
		t.Fatalf("join an imported session: %v", err)
	}
	if _, err := s.UpdateMinutes(ctx, &sessionv1.UpdateMinutesRequest{SessionId: im.sid, CommandId: ids.NewCommandID(),
		ParticipantId: "scribe", Text: "# Design review\n\n- Ship on Friday."}); err != nil {
		t.Fatal(err)
	}
	if _, err := im.a.Decision.RecordDecision(ctx, &decisionv1.RecordDecisionRequest{CommandId: ids.NewCommandID(),
		Text: "We ship on Friday.", AuthorParticipantId: "scribe", SessionId: im.sid}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndSession(ctx, &sessionv1.EndSessionRequest{SessionId: im.sid, CommandId: ids.NewCommandID(),
		ParticipantId: "scribe"}); err != nil {
		t.Fatal(err)
	}
	m, err := im.a.Minutes.GetMinutes(ctx, &minutesv1.GetMinutesRequest{SessionId: im.sid})
	if err != nil || m.GetMinutes().GetText() != "# Design review\n\n- Ship on Friday." {
		t.Fatalf("minutes of the imported session: %v, %v", m, err)
	}
	ds, err := im.a.Decision.ListDecisions(ctx, &decisionv1.ListDecisionsRequest{SessionId: im.sid})
	if err != nil || len(ds.GetDecisions()) != 1 {
		t.Fatalf("decisions of the imported session: %v, %v", ds, err)
	}
}

// TestImportRules: what a page must contain, and when the hub rejects it.
func TestImportRules(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	im := newImporter(t, srv, "importer")
	other := srv.Participant(t, "other", "Other", human)
	first := func() *sessionimportv1.ImportConversationRequest {
		return &sessionimportv1.ImportConversationRequest{Conversation: conversation(), Participants: people(),
			Utterances: []*sessionimportv1.ImportedUtterance{utt("ada", "Hello.")}}
	}
	bad := map[string]func(r *sessionimportv1.ImportConversationRequest){
		"no conversation": func(r *sessionimportv1.ImportConversationRequest) { r.Conversation = nil },
		"no source":       func(r *sessionimportv1.ImportConversationRequest) { r.Conversation.Source = " " },
		"bad key":         func(r *sessionimportv1.ImportConversationRequest) { r.Participants[0].Key = "a:b" },
		"repeated key":    func(r *sessionimportv1.ImportConversationRequest) { r.Participants[1].Key = "ada" },
		"no name":         func(r *sessionimportv1.ImportConversationRequest) { r.Participants[0].DisplayName = "" },
		"no kind":         func(r *sessionimportv1.ImportConversationRequest) { r.Participants[0].Kind = 0 },
		"unknown speaker": func(r *sessionimportv1.ImportConversationRequest) { r.Utterances[0].Speaker = "nobody" },
		"no text":         func(r *sessionimportv1.ImportConversationRequest) { r.Utterances[0].Text = " " },
		"no modality":     func(r *sessionimportv1.ImportConversationRequest) { r.Utterances[0].Modality = 0 },
		"too many utterances": func(r *sessionimportv1.ImportConversationRequest) {
			r.Utterances = make([]*sessionimportv1.ImportedUtterance, 1001)
		},
	}
	for name, change := range bad {
		r := first()
		change(r)
		if _, err := im.page(r); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: want invalid_argument, got %v", name, err)
		}
	}
	if _, err := im.a.Import.ImportConversation(ctx10(t), &sessionimportv1.ImportConversationRequest{CommandId: ids.NewCommandID(),
		ParticipantId: "importer", Conversation: conversation()}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("no session: want invalid_argument, got %v", err)
	}
	if evs := im.events(); len(evs) != 0 {
		t.Fatalf("rejected pages stored events: %v", evs)
	}
	// Only the participant of the token imports.
	r := first()
	r.ParticipantId = "other"
	im.fails(connect.CodePermissionDenied, r)

	start := first()
	start.CommandId = ids.NewCommandID()
	im.ok(start)
	ctx := ctx10(t)
	// While the import runs, the session takes no session commands, and no pages of others.
	if _, err := other.Session.Join(ctx, &sessionv1.JoinRequest{SessionId: im.sid, CommandId: ids.NewCommandID()}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("join during the import: want failed_precondition, got %v", err)
	}
	if _, err := other.Import.ImportConversation(ctx, &sessionimportv1.ImportConversationRequest{SessionId: im.sid,
		CommandId: ids.NewCommandID(), ParticipantId: "other", Utterances: []*sessionimportv1.ImportedUtterance{utt("ada", "Mine.")}}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("page of another participant: want permission_denied, got %v", err)
	}
	// A later page starts no new import, adds no known speaker again, and is not empty.
	again := first()
	again.Participants = nil
	im.fails(connect.CodeFailedPrecondition, again)
	im.fails(connect.CodeInvalidArgument, &sessionimportv1.ImportConversationRequest{
		Participants: []*sessionimportv1.ImportedParticipant{{Key: "ada", DisplayName: "Ada", Kind: pHuman}}})
	im.fails(connect.CodeInvalidArgument, &sessionimportv1.ImportConversationRequest{})
	// The command ID of a page is not a session command.
	if _, err := im.a.Session.Join(ctx, &sessionv1.JoinRequest{SessionId: im.sid, CommandId: start.GetCommandId()}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("join with the command ID of a page: want already_exists, got %v", err)
	}
	done := im.ok(&sessionimportv1.ImportConversationRequest{Complete: true})
	// After the import: no more pages; session commands work.
	im.fails(connect.CodeFailedPrecondition, &sessionimportv1.ImportConversationRequest{Utterances: []*sessionimportv1.ImportedUtterance{utt("ada", "Late.")}})
	if _, err := other.Session.Join(ctx, &sessionv1.JoinRequest{SessionId: im.sid, CommandId: ids.NewCommandID()}); err != nil {
		t.Errorf("join after the import: %v", err)
	}
	// Nobody can mark an imported sentence off the record: its speaker has no key (#50).
	if _, err := im.a.Session.Join(ctx, &sessionv1.JoinRequest{SessionId: im.sid, CommandId: ids.NewCommandID()}); err != nil {
		t.Fatal(err)
	}
	if _, err := im.a.Session.MarkOffTheRecord(ctx, &sessionv1.MarkOffTheRecordRequest{SessionId: im.sid, CommandId: ids.NewCommandID(),
		ParticipantId: "importer", UtteranceEventId: done.GetFirstEventId() - 1}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("mark an imported sentence off the record: want permission_denied, got %v", err)
	}

	// A session with live events cannot be imported into.
	live := newImporter(t, srv, "live")
	if _, err := live.a.Session.Join(ctx, &sessionv1.JoinRequest{SessionId: live.sid, CommandId: ids.NewCommandID()}); err != nil {
		t.Fatal(err)
	}
	live.fails(connect.CodeFailedPrecondition, first())
	live.fails(connect.CodeFailedPrecondition, &sessionimportv1.ImportConversationRequest{Utterances: []*sessionimportv1.ImportedUtterance{utt("ada", "Hi.")}})

	// A stage link cannot import.
	link, err := im.a.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{SessionId: im.sid, CommandId: ids.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = srv.WithToken(t, stageToken(t, link.GetUrl())).Import.ImportConversation(ctx, &sessionimportv1.ImportConversationRequest{SessionId: "x",
		CommandId: ids.NewCommandID(), ParticipantId: "importer", Conversation: conversation()})
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Errorf("import with a stage link: want permission_denied, got %v", err)
	}
	// Without a token nothing is imported.
	if _, err := srv.Anonymous(t).Import.ImportConversation(ctx, &sessionimportv1.ImportConversationRequest{SessionId: "x",
		CommandId: ids.NewCommandID(), ParticipantId: "importer", Conversation: conversation()}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("import without a token: want unauthenticated, got %v", err)
	}
}

// TestImportSurvivesRestart: an import that is not complete is restored from the archive after a
// clean stop of the hub, and continues with its next page.
func TestImportSurvivesRestart(t *testing.T) {
	srv := contracttest.StartHub(t, nil, "-archive-interval", "1h")
	im := newImporter(t, srv, "importer")
	im.ok(&sessionimportv1.ImportConversationRequest{Conversation: conversation(), Participants: people(),
		Utterances: []*sessionimportv1.ImportedUtterance{utt("ada", "Before the restart.")}})
	if code, err := srv.Stop(10 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop the hub: exit %d, %v", code, err)
	}
	srv2 := contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil, "-archive-interval", "1h")
	im.a = srv2.WithKey(t, im.a.Key)
	if _, err := im.a.Session.Join(ctx10(t), &sessionv1.JoinRequest{SessionId: im.sid, CommandId: ids.NewCommandID()}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("join a restored import that is not complete: want failed_precondition, got %v", err)
	}
	res := im.ok(&sessionimportv1.ImportConversationRequest{Utterances: []*sessionimportv1.ImportedUtterance{utt("claude", "After it.")},
		Complete: true})
	if res.GetFirstEventId() != 5 || res.GetLastEventId() != 6 || !res.GetComplete() {
		t.Fatalf("page after the restart: %v", res)
	}
	evs := im.events()
	if err := merkle.VerifyEvents(evs, res.GetTreeHead(), hubKey(t, im.a.Session)); err != nil {
		t.Fatal(err)
	}
	if evs[0].GetType() != events.SessionImported || evs[5].GetImportCompleted().GetUtterances() != 2 {
		t.Fatalf("restored import: %v", evs)
	}
}
