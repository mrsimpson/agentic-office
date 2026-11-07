package contract

// Contract tests of the restore after a restart of the hub (dec-restore-from-peers) through
// if-session-commands and if-session-stream. The hub restores every session from the archive;
// the events after the last write are lost and a peer offers them with OfferLog. The tests lose
// those events on purpose: a git wrapper makes the commits fail, then the hub is killed.

import (
	"context"
	"crypto/ed25519"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/hub/session/api/merkle"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// peerCopy is what a peer holds: the events it received from the stream and the signed tree head
// that came with the last of them.
type peerCopy struct {
	events []*sessionv1.Event
	head   *sessionv1.SignedTreeHead
}

// follow subscribes after cursor 0 and reads n events. The head is the one on the last event.
func follow(t *testing.T, a *contracttest.Actor, sid string, n int) peerCopy {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := a.Session.Subscribe(ctx, &sessionv1.SubscribeRequest{SessionId: sid})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var c peerCopy
	for len(c.events) < n {
		res, err := st.Receive()
		if err != nil {
			t.Fatalf("receive event %d of %d: %v", len(c.events)+1, n, err)
		}
		c.events = append(c.events, res.GetEvent())
		if h := res.GetTreeHead(); h != nil {
			if h.GetTreeSize() != res.GetEvent().GetId() {
				t.Fatalf("tree head of size %d on event %d", h.GetTreeSize(), res.GetEvent().GetId())
			}
			c.head = h
		}
	}
	return c
}

// lost is a session whose last events the hub lost in a crash.
type lost struct {
	t        *testing.T
	srv      *contracttest.Hub // the restarted hub
	sid      string
	ada      *contracttest.Actor // a participant after the restart
	key      ed25519.PublicKey
	archived int      // events in the archive
	live     peerCopy // followed live: the original of the sentence that went off the record
	late     peerCopy // read after the mark: a placeholder for it
	short    *sessionv1.SignedTreeHead
}

// loseTail runs a session on a hub that writes it to the archive, then stops the writes, adds
// events, and kills the hub. It restarts the hub on the same port and data directory with
// restoreWait.
func loseTail(t *testing.T, restoreWait string) *lost {
	t.Helper()
	gate := contracttest.NewGitGate(t)
	srv := contracttest.StartHub(t, gate.Env, "-archive-interval", "200ms")
	ada := srv.Participant(t, "ada", "Ada", human)
	bot := srv.Participant(t, "bot", "Bot", agent)
	m := &meeting{t: t, srv: srv, sid: "s-" + ids.NewCommandID(), actors: map[string]*contracttest.Actor{"ada": ada, "bot": bot}, reader: bot}
	for _, a := range []*contracttest.Actor{ada, bot} {
		if _, err := a.Session.Join(m.ctx(), &sessionv1.JoinRequest{SessionId: m.sid, CommandId: ids.NewCommandID()}); err != nil {
			t.Fatal(err)
		}
	}
	m.say("ada", "Archived one.")
	last := m.say("ada", "Archived two.")
	contracttest.WaitArchived(t, srv.ArchiveDir(), m.sid, 10*time.Second, func(a *contracttest.ArchivedSession) bool {
		return len(a.Events) == int(last.GetId())
	})
	l := &lost{t: t, sid: m.sid, key: hubKey(t, ada.Session), archived: int(last.GetId())}
	l.short = m.head(uint32(l.archived - 1))

	// From now on nothing reaches the archive.
	gate.Close(t)
	liveDone := make(chan peerCopy, 1)
	const tail = 4 // three utterances and one mark
	go func() { liveDone <- follow(t, bot, m.sid, l.archived+tail) }()
	m.say("ada", "Lost one.")
	secret := m.say("ada", "Lost secret.")
	time.Sleep(100 * time.Millisecond) // let the live peer receive the original
	if _, err := ada.Session.MarkOffTheRecord(m.ctx(), &sessionv1.MarkOffTheRecordRequest{SessionId: m.sid,
		CommandId: ids.NewCommandID(), ParticipantId: "ada", UtteranceEventId: secret.GetId()}); err != nil {
		t.Fatal(err)
	}
	m.say("ada", "Lost three.")
	l.live = <-liveDone
	l.late = follow(t, ada, m.sid, l.archived+tail)
	for _, c := range []peerCopy{l.live, l.late} {
		if err := merkle.VerifyEvents(c.events, c.head, l.key); err != nil {
			t.Fatalf("the head of the stream does not cover the copy: %v", err)
		}
	}
	if !l.live.events[secret.GetId()-1].GetUtteranceFinal().GetOffTheRecord() &&
		!l.late.events[secret.GetId()-1].GetUtteranceFinal().GetOffTheRecord() {
		t.Fatal("the late copy has no placeholder")
	}

	srv.Kill()
	gate.Open(t)
	l.srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, gate.Env,
		"-addr", srv.Addr(), "-archive-interval", "200ms", "-restore-wait", restoreWait)
	l.ada = l.srv.WithKey(t, ada.Key)
	return l
}

func (l *lost) offer(c peerCopy, after int) (*sessionv1.OfferLogResponse, error) {
	return l.ada.Session.OfferLog(ctx10(l.t), &sessionv1.OfferLogRequest{SessionId: l.sid,
		AfterEventId: uint32(after), Events: c.events[after:c.head.GetTreeSize()], TreeHead: c.head})
}

func (l *lost) events() []*sessionv1.Event {
	l.t.Helper()
	res, err := l.ada.Session.GetEvents(ctx10(l.t), &sessionv1.GetEventsRequest{SessionId: l.sid, Limit: 1000})
	if err != nil {
		l.t.Fatal(err)
	}
	return res.GetEvents()
}

func (l *lost) say(text string) (*sessionv1.Event, error) {
	res, err := l.ada.Session.SubmitUtterance(ctx10(l.t), &sessionv1.SubmitUtteranceRequest{SessionId: l.sid,
		CommandId: ids.NewCommandID(), ParticipantId: "ada", Text: text, Modality: sessionv1.Modality_MODALITY_TEXT})
	return res.GetResult().GetEvent(), err
}

// The hub restores the session from the archive and waits for a peer. A copy shorter than the
// archive is a no-op, a forged copy fails, and of two peers that offer at once one fills in and
// the other is a no-op. Then the IDs continue and the cursors of the peers stay valid.
func TestRestoreFromArchiveThenPeer(t *testing.T) {
	l := loseTail(t, "1m")
	n := len(l.live.events)

	// The archive part is back, with the same events.
	got := l.events()
	if len(got) != l.archived {
		t.Fatalf("restored %d events, the archive has %d", len(got), l.archived)
	}
	for i, ev := range got {
		if !proto.Equal(ev, l.live.events[i]) {
			t.Fatalf("restored event %d:\n got %v\nwant %v", i+1, ev, l.live.events[i])
		}
	}
	// A peer with a longer copy is beyond the end; commands wait for the peers.
	st, err := l.ada.Session.Subscribe(ctx10(t), &sessionv1.SubscribeRequest{SessionId: l.sid, AfterEventId: uint32(n)})
	if err == nil {
		_, err = st.Receive()
		st.Close()
	}
	wantCode(t, "cursor of a peer before the fill-in", err, connect.CodeOutOfRange)
	_, err = l.say("Too early.")
	wantCode(t, "command before the fill-in", err, connect.CodeUnavailable)

	// Shorter than the archive: a no-op.
	short := peerCopy{events: l.live.events, head: l.short}
	res, err := l.offer(short, 0)
	if err != nil || res.GetAccepted() || res.GetTreeHead().GetTreeSize() != uint32(l.archived) {
		t.Fatalf("shorter copy: %v, %v", res, err)
	}

	// Forged: a changed event, a changed signature, a head of another session.
	forged := peerCopy{events: append([]*sessionv1.Event{}, l.live.events...), head: l.live.head}
	ev := proto.Clone(forged.events[l.archived]).(*sessionv1.Event)
	ev.GetUtteranceFinal().Text = "Never said."
	forged.events[l.archived] = ev
	_, err = l.offer(forged, l.archived)
	wantCode(t, "copy with a changed event", err, connect.CodeInvalidArgument)
	badSig := proto.Clone(l.live.head).(*sessionv1.SignedTreeHead)
	badSig.Signature[0] ^= 1
	_, err = l.offer(peerCopy{events: l.live.events, head: badSig}, l.archived)
	wantCode(t, "copy with a forged signature", err, connect.CodeInvalidArgument)
	_, err = l.ada.Session.OfferLog(ctx10(t), &sessionv1.OfferLogRequest{SessionId: l.sid, AfterEventId: uint32(l.archived),
		Events: l.live.events[l.archived : n-1], TreeHead: l.live.head})
	wantCode(t, "copy with fewer events than its head", err, connect.CodeInvalidArgument)
	if got := l.events(); len(got) != l.archived {
		t.Fatalf("a rejected copy changed the log: %d events", len(got))
	}

	// Two peers at once: one with the original of the sentence off the record, offering the
	// tail; one with the placeholder, offering the whole log. One fills in, the other is a no-op.
	var wg sync.WaitGroup
	results := make([]*sessionv1.OfferLogResponse, 2)
	errs := make([]error, 2)
	for i, o := range []struct {
		c     peerCopy
		after int
	}{{l.live, l.archived}, {l.late, 0}} {
		wg.Add(1)
		go func() { defer wg.Done(); results[i], errs[i] = l.offer(o.c, o.after) }()
	}
	wg.Wait()
	accepted := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("offer %d: %v", i+1, errs[i])
		}
		if results[i].GetAccepted() {
			accepted++
		}
		if results[i].GetTreeHead().GetTreeSize() != uint32(n) {
			t.Errorf("offer %d: hub has %d events, want %d", i+1, results[i].GetTreeHead().GetTreeSize(), n)
		}
	}
	if accepted != 1 {
		t.Fatalf("%d offers accepted, want exactly 1", accepted)
	}
	// The log is the one the peers had: as the interfaces deliver it, with the placeholder.
	got = l.events()
	if len(got) != n {
		t.Fatalf("after the fill-in the hub has %d events, want %d", len(got), n)
	}
	for i, ev := range got {
		if !proto.Equal(ev, l.late.events[i]) {
			t.Fatalf("event %d after the fill-in:\n got %v\nwant %v", i+1, ev, l.late.events[i])
		}
	}
	if err := merkle.VerifyEvents(got, l.late.head, l.key); err != nil {
		t.Fatalf("the restored log does not verify against the head of the peer: %v", err)
	}
	// A repeated offer is a no-op.
	if res, err := l.offer(l.live, l.archived); err != nil || res.GetAccepted() {
		t.Fatalf("repeated offer: %v, %v", res, err)
	}

	// The IDs continue; the cursor of a peer is valid again and the stream has no duplicate.
	next, err := l.say("After the restore.")
	if err != nil {
		t.Fatal(err)
	}
	if next.GetId() != uint32(n+1) {
		t.Fatalf("next event has ID %d, want %d", next.GetId(), n+1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err = l.ada.Session.Subscribe(ctx, &sessionv1.SubscribeRequest{SessionId: l.sid, AfterEventId: uint32(n)})
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.Receive()
	st.Close()
	if err != nil || r.GetEvent().GetId() != uint32(n+1) {
		t.Fatalf("stream from the cursor of a peer: %v, %v", r, err)
	}

	// The archive gets the whole log, which verifies.
	if _, err := l.ada.Session.EndSession(ctx10(t), &sessionv1.EndSessionRequest{SessionId: l.sid,
		CommandId: ids.NewCommandID(), ParticipantId: "ada"}); err != nil {
		t.Fatal(err)
	}
	a := contracttest.WaitArchived(t, l.srv.ArchiveDir(), l.sid, 10*time.Second, (*contracttest.ArchivedSession).Ended)
	a.Verify(t, l.sid, l.key)
	if len(a.Events) != n+2 {
		t.Fatalf("archive has %d events, want %d", len(a.Events), n+2)
	}
}

// When nobody fills in within the restore wait, the hub takes commands again and continues after
// the archive. A copy of the old tail then forks from the log and fails.
func TestRestoreForkedCopyFails(t *testing.T) {
	l := loseTail(t, "500ms")
	var next *sessionv1.Event
	waitUntil(t, 10*time.Second, "the hub takes commands after the restore wait", func() bool {
		ev, err := l.say("Another sentence.")
		if connect.CodeOf(err) == connect.CodeUnavailable {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		next = ev
		return true
	})
	if next.GetId() != uint32(l.archived+1) {
		t.Fatalf("first event after the wait has ID %d, want %d", next.GetId(), l.archived+1)
	}
	_, err := l.offer(l.live, l.archived)
	wantCode(t, "tail that forks", err, connect.CodeFailedPrecondition)
	_, err = l.offer(l.late, 0)
	wantCode(t, "whole log that forks", err, connect.CodeFailedPrecondition)
	if got := l.events(); len(got) != l.archived+1 {
		t.Fatalf("a forked copy changed the log: %d events", len(got))
	}
}

// After a stop with a last write, the archive is complete: the hub restores every session, ended
// or not, and takes commands at once. A stage link cannot offer a copy.
func TestRestoreAfterCleanStop(t *testing.T) {
	srv := contracttest.StartHub(t, nil, "-archive-interval", "1h")
	ada := srv.Participant(t, "ada", "Ada", identityv1.ActorKind_ACTOR_KIND_HUMAN)
	running, ended := "s-"+ids.NewCommandID(), "s-"+ids.NewCommandID()
	var last uint32
	for _, sid := range []string{running, ended} {
		if _, err := ada.Session.Join(ctx10(t), &sessionv1.JoinRequest{SessionId: sid, CommandId: ids.NewCommandID()}); err != nil {
			t.Fatal(err)
		}
		res, err := ada.Session.SubmitUtterance(ctx10(t), &sessionv1.SubmitUtteranceRequest{SessionId: sid,
			CommandId: ids.NewCommandID(), ParticipantId: "ada", Text: "Before the stop.", Modality: sessionv1.Modality_MODALITY_TEXT})
		if err != nil {
			t.Fatal(err)
		}
		last = res.GetResult().GetEvent().GetId()
	}
	repeat := &sessionv1.SubmitUtteranceRequest{SessionId: running, CommandId: ids.NewCommandID(), ParticipantId: "ada",
		Text: "Repeated after the restart.", Modality: sessionv1.Modality_MODALITY_TEXT}
	first, err := ada.Session.SubmitUtterance(ctx10(t), repeat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ada.Session.EndSession(ctx10(t), &sessionv1.EndSessionRequest{SessionId: ended,
		CommandId: ids.NewCommandID(), ParticipantId: "ada"}); err != nil {
		t.Fatal(err)
	}
	if code, err := srv.Stop(10 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: exit %d, %v", code, err)
	}

	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil, "-archive-interval", "1h", "-restore-wait", "1h")
	ada = srv.WithKey(t, ada.Key)
	// Idempotency covers the events from before the restart.
	again, err := ada.Session.SubmitUtterance(ctx10(t), repeat)
	if err != nil || !again.GetResult().GetDuplicate() || !proto.Equal(again.GetResult().GetEvent(), first.GetResult().GetEvent()) {
		t.Fatalf("repeated command after the restart: %v, %v", again, err)
	}
	res, err := ada.Session.SubmitUtterance(ctx10(t), &sessionv1.SubmitUtteranceRequest{SessionId: running,
		CommandId: ids.NewCommandID(), ParticipantId: "ada", Text: "After the restart.", Modality: sessionv1.Modality_MODALITY_TEXT})
	if err != nil {
		t.Fatalf("command after a clean restart: %v", err)
	}
	if res.GetResult().GetEvent().GetId() != first.GetResult().GetEvent().GetId()+1 {
		t.Fatalf("ID after the restart %d, want %d", res.GetResult().GetEvent().GetId(), first.GetResult().GetEvent().GetId()+1)
	}
	_, err = ada.Session.SubmitUtterance(ctx10(t), &sessionv1.SubmitUtteranceRequest{SessionId: ended,
		CommandId: ids.NewCommandID(), ParticipantId: "ada", Text: "Too late.", Modality: sessionv1.Modality_MODALITY_TEXT})
	wantCode(t, "command in an ended session after the restart", err, connect.CodeFailedPrecondition)
	evs, err := ada.Session.GetEvents(ctx10(t), &sessionv1.GetEventsRequest{SessionId: ended})
	if err != nil || len(evs.GetEvents()) != int(last)+1 || evs.GetEvents()[last].GetSessionEnded() == nil {
		t.Fatalf("ended session after the restart: %v, %v", evs, err)
	}

	link, err := ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: running, CommandId: ids.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	head, err := ada.Session.GetTreeHead(ctx10(t), &sessionv1.GetTreeHeadRequest{SessionId: running})
	if err != nil {
		t.Fatal(err)
	}
	_, err = srv.WithToken(t, stageToken(t, link.GetUrl())).Session.OfferLog(ctx10(t), &sessionv1.OfferLogRequest{SessionId: running,
		AfterEventId: head.GetTreeHead().GetTreeSize(), TreeHead: head.GetTreeHead()})
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied && code != connect.CodeUnauthenticated {
		t.Errorf("offer with a stage link: %v", err)
	}
}
