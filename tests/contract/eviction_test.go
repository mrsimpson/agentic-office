package contract

// Contract tests of the sessions that leave memory (dec-session-eviction, issue #113). A session
// that has ended, is in the archive and is read by nobody leaves the memory of the hub; a later
// call loads it from the archive again, with the same events and the same signed tree head. The
// tests see it through the log of the hub ("left memory") and through the interfaces.

import (
	"regexp"
	"strings"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// leftMemory counts the lines "session … left memory" of the hub for a session.
func leftMemory(srv *contracttest.Hub, sid string) int {
	return strings.Count(srv.Stderr(), `session "`+sid+`" left memory`)
}

// waitLeftMemory waits until the hub has taken the session out of memory n times.
func waitLeftMemory(t *testing.T, srv *contracttest.Hub, sid string, n int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for leftMemory(srv, sid) < n {
		if time.Now().After(deadline) {
			t.Fatalf("session %q did not leave memory %d times:\n%s", sid, n, srv.Stderr())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func sameEvents(t *testing.T, what string, a, b []*sessionstreamv1.Event) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s: %d events, want %d", what, len(b), len(a))
	}
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			t.Fatalf("%s: event %d is %v, want %v", what, i+1, b[i], a[i])
		}
	}
}

// An ended session leaves memory once it is archived and nobody reads it. The next call loads it
// from the archive: the same events (also the placeholder of a sentence off the record), the same
// signed tree head, and every rule of an ended session still holds.
func TestEndedSessionLeavesMemoryAndLoadsAgain(t *testing.T) {
	m := newMeetingArgs(t, "-archive-interval", "100ms")
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "Public.")
	said := m.utterance(ada, "Secret.")
	res, err := m.s(ada).SubmitUtterance(m.ctx(), said)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.s(ada).MarkOffTheRecord(m.ctx(), &sessioncommandsv1.MarkOffTheRecordRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: ada, UtteranceEventId: res.GetResult().GetEvent().GetId()}); err != nil {
		t.Fatal(err)
	}
	endCmd := coreapi.NewCommandID()
	endReq := &sessioncommandsv1.EndSessionRequest{SessionId: m.sid, CommandId: endCmd, ParticipantId: ada}
	ended, err := m.s(ada).EndSession(m.ctx(), endReq)
	if err != nil {
		t.Fatal(err)
	}

	before, headBefore := m.events(), m.head(0)
	waitLeftMemory(t, m.srv, m.sid, 1)

	// Read again: loaded from the archive, then out of memory again.
	after, headAfter := m.events(), m.head(0)
	sameEvents(t, "events after the session left memory", before, after)
	if !proto.Equal(headBefore, headAfter) {
		t.Fatalf("tree head after the session left memory: %v, want %v", headAfter, headBefore)
	}
	if n := len(after); n < 4 || !after[2].GetUtteranceFinal().GetOffTheRecord() || after[2].GetUtteranceFinal().GetText() != "" {
		t.Fatalf("the sentence off the record is no placeholder: %v", after)
	}
	waitLeftMemory(t, m.srv, m.sid, 2)

	// The rules of an ended session hold for the loaded session.
	s, ctx, cmd := m.s(ada), m.ctx(), coreapi.NewCommandID
	_, err = s.SubmitUtterance(ctx, m.utterance(ada, "too late"))
	wantCode(t, "utterance after the end", err, connect.CodeFailedPrecondition)
	late := m.register("Late", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	_, err = late.Commands.Join(ctx, &sessioncommandsv1.JoinRequest{SessionId: m.sid, CommandId: cmd()})
	wantCode(t, "join after the end", err, connect.CodeFailedPrecondition)
	_, err = s.EndSession(ctx, &sessioncommandsv1.EndSessionRequest{SessionId: m.sid, CommandId: cmd(), ParticipantId: ada})
	wantCode(t, "end twice", err, connect.CodeFailedPrecondition)
	// Commands from before the end can still be repeated and give the first answer.
	again, err := s.EndSession(ctx, endReq)
	if err != nil || !again.GetResult().GetDuplicate() || again.GetResult().GetEvent().GetId() != ended.GetResult().GetEvent().GetId() {
		t.Fatalf("repeated end: %v, %v", again, err)
	}
	repeated, err := s.SubmitUtterance(ctx, said)
	if err != nil || !repeated.GetResult().GetDuplicate() || repeated.GetResult().GetEvent().GetId() != res.GetResult().GetEvent().GetId() {
		t.Fatalf("repeated utterance: %v, %v", repeated, err)
	}

	// A new subscriber gets the whole log from the archive and the stream closes after session.ended.
	log := m.readAll(0)
	checkLog(t, m.sid, log)
	if last := log[len(log)-1]; last.GetType() != events.SessionEnded {
		t.Fatalf("last event: %v", last)
	}
	// The proofs of the loaded session verify against the signed head.
	if proof := m.head(0); proof.GetTreeSize() != uint32(len(log)) {
		t.Fatalf("tree head of the loaded session: %v", proof)
	}
}

// A session ID of an ended session stays taken after the session left memory: it is not a new
// session.
func TestEvictedSessionIDIsNotNew(t *testing.T) {
	m := newMeetingArgs(t, "-archive-interval", "100ms")
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.end(ada)
	waitLeftMemory(t, m.srv, m.sid, 1)
	other := m.register("Other", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	_, err := other.Commands.Join(m.ctx(), &sessioncommandsv1.JoinRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID()})
	wantCode(t, "join the session that left memory", err, connect.CodeFailedPrecondition)
	if log := m.events(); len(log) != 2 {
		t.Fatalf("log of the session after the failed join: %v", log)
	}
}

// After a restart, the hub restores the sessions that have not ended. Ended sessions stay in the
// archive and load when a call needs them.
func TestRestartLeavesEndedSessionsInTheArchive(t *testing.T) {
	m := newMeetingArgs(t, "-archive-interval", "100ms")
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "Before the end.")
	endedSID := m.sid
	m.end(ada)
	endedLog := m.events()
	waitLeftMemory(t, m.srv, endedSID, 1)

	// A second session stays open.
	open := &meeting{t: t, srv: m.srv, sid: "open-" + coreapi.NewCommandID(), actors: m.actors, reader: m.reader}
	open.join("Bea", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	open.say("bea", "Still talking.")
	contracttest.WaitArchived(t, m.srv.ArchiveDir(), open.sid, 20*time.Second, func(a *contracttest.ArchivedSession) bool { return len(a.Events) == 2 })

	if code, err := m.srv.Stop(10 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	srv := contracttest.StartHubIn(t, m.srv.DataDir, m.srv.AdminKey, nil, "-archive-interval", "100ms")
	if !regexp.MustCompile(`restore: 1 sessions from the archive.* 1 ended sessions stay in the archive`).MatchString(srv.Stderr()) {
		t.Fatalf("restore did not load one open session and leave one ended:\n%s", srv.Stderr())
	}
	reader := srv.WithKey(t, m.reader.Key)
	got, err := reader.Stream.GetEvents(m.ctx(), &sessionstreamv1.GetEventsRequest{SessionId: endedSID, Limit: 1000})
	if err != nil {
		t.Fatalf("read the ended session after the restart: %v", err)
	}
	sameEvents(t, "ended session after the restart", endedLog, got.GetEvents())
	bea := srv.WithKey(t, m.actors["bea"].Key)
	if _, err := bea.Commands.SubmitUtterance(m.ctx(), &sessioncommandsv1.SubmitUtteranceRequest{SessionId: open.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: "bea", Text: "Back again.", Modality: sessionstreamv1.Modality_MODALITY_TEXT}); err != nil {
		// A restored session that has not ended may wait for a peer for a while.
		wantCode(t, "command in the restored open session", err, connect.CodeUnavailable)
	}
}
