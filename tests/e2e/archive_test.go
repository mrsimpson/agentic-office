//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// slowCommit is how long every commit of the archive takes in qs-archive-no-backpressure.
const slowCommit = 5 * time.Second

// slowGit writes a git wrapper that makes every "git commit" take d and logs when each commit
// starts and ends, and whether it is a session write (message "Sessions: …") or a record. It
// returns the directory to put first in PATH and the log file.
func slowGit(t *testing.T, d time.Duration) (string, string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	dir := filepath.Join(t.TempDir(), "slow-git")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(dir, "commits.log")
	script := fmt.Sprintf(`#!/bin/sh
# The commits of Identity (message "System: …") are not slowed and not counted.
case "$*" in *" -m System:"*) exec %[2]q "$@";; esac
if [ "$1" = commit ]; then
  kind=record
  case "$*" in *" -m Sessions:"*) kind=session;; esac
  echo "start $(date +%%s%%N) $kind" >> %[1]q
  sleep %[3]d
  %[2]q "$@"; rc=$?
  echo "end $(date +%%s%%N) $kind" >> %[1]q
  exit $rc
fi
exec %[2]q "$@"
`, logFile, real, int(d.Seconds()))
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, logFile
}

// commitSpan is one commit in the log of slowGit. end is zero while the commit runs.
type commitSpan struct {
	start, end time.Time
	session    bool
}

// commitSpans reads the commits from the log of slowGit, oldest first.
func commitSpans(t *testing.T, logFile string) []commitSpan {
	t.Helper()
	b, err := os.ReadFile(logFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	var spans []commitSpan
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		ns, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			t.Fatalf("commit log: %q", line)
		}
		at := time.Unix(0, ns)
		switch f[0] {
		case "start":
			spans = append(spans, commitSpan{start: at, session: f[2] == "session"})
		case "end":
			if len(spans) == 0 || !spans[len(spans)-1].end.IsZero() {
				t.Fatalf("two commits at the same time (an end without its start):\n%s", b)
			}
			spans[len(spans)-1].end = at
		}
	}
	return spans
}

// qs-archive-no-backpressure: every commit of the archive takes 5 s while a participant speaks
// every 100 ms and the interval is 500 ms. Commands and the stream run as usual, at most one
// commit runs at a time, writes do not queue up (fewer commits than intervals), and the last
// write has the whole session with a tree head that verifies. The latency target is asserted
// only with the build tag performance.
func TestQualityArchiveNoBackpressure(t *testing.T) {
	slow(t)
	gitDir, commitLog := slowGit(t, slowCommit)
	duration := qsDuration(t, "QS_ARCHIVE_DURATION", 12*time.Second)
	srv := contracttest.StartHub(t, []string{"PATH=" + gitDir + string(os.PathListSeparator) + os.Getenv("PATH")},
		"-archive-interval", "500ms")
	ada := srv.Participant(t, "ada", "Ada", identityv1.ActorKind_ACTOR_KIND_HUMAN)
	bot := srv.Participant(t, "bot", "Bot", identityv1.ActorKind_ACTOR_KIND_AGENT)
	sid := "archive-" + coreapi.NewCommandID()
	ctx, cancel := context.WithTimeout(context.Background(), duration+2*time.Minute)
	defer cancel()
	if _, err := ada.Commands.Join(ctx, &sessioncommandsv1.JoinRequest{SessionId: sid, CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatal(err)
	}

	// The bot follows the stream and notes when each event arrives.
	var mu sync.Mutex
	arrived := map[uint32]time.Time{}
	stream, err := bot.Stream.Subscribe(ctx, &sessionstreamv1.SubscribeRequest{SessionId: sid})
	if err != nil {
		t.Fatal(err)
	}
	streamDone := make(chan error, 1)
	go func() {
		defer stream.Close()
		for {
			res, err := stream.Receive()
			if err != nil {
				streamDone <- err
				return
			}
			mu.Lock()
			arrived[res.GetEvent().GetId()] = time.Now()
			mu.Unlock()
			if res.GetEvent().GetSessionEnded() != nil {
				streamDone <- nil
				return
			}
		}
	}()

	var command, toStream latencies
	sent := map[uint32]time.Time{}
	start := time.Now()
	for i := 0; time.Since(start) < duration; i++ {
		t0 := time.Now()
		res, err := ada.Commands.SubmitUtterance(ctx, &sessioncommandsv1.SubmitUtteranceRequest{SessionId: sid,
			CommandId: coreapi.NewCommandID(), ParticipantId: "ada", Text: fmt.Sprintf("Sentence %d.", i),
			Modality: sessionstreamv1.Modality_MODALITY_TEXT})
		if err != nil {
			t.Fatalf("utterance %d: %v", i, err)
		}
		command = append(command, time.Since(t0))
		sent[res.GetResult().GetEvent().GetId()] = t0
		time.Sleep(100 * time.Millisecond)
	}
	spoken := time.Since(start)
	if _, err := ada.Commands.EndSession(ctx, &sessioncommandsv1.EndSessionRequest{SessionId: sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: "ada"}); err != nil {
		t.Fatal(err)
	}
	if err := <-streamDone; err != nil {
		t.Fatalf("stream: %v", err)
	}
	mu.Lock()
	for id, t0 := range sent {
		at, ok := arrived[id]
		if !ok {
			t.Fatalf("event %d did not reach the stream", id)
		}
		toStream = append(toStream, at.Sub(t0))
	}
	mu.Unlock()

	// The last write has the whole session.
	repo := srv.ArchiveDir()
	last := contracttest.WaitArchived(t, repo, sid, 4*slowCommit, (*contracttest.ArchivedSession).Ended)
	evs, err := ada.Stream.GetEvents(ctx, &sessionstreamv1.GetEventsRequest{SessionId: sid, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Events) != len(evs.GetEvents()) {
		t.Fatalf("archive has %d events, the session %d", len(last.Events), len(evs.GetEvents()))
	}
	keyRes, err := ada.Commands.GetHubKey(ctx, &sessioncommandsv1.GetHubKeyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	commits := contracttest.SessionCommits(t, repo, sid)
	for _, c := range commits {
		contracttest.ReadArchivedSession(t, repo, sid, c).Verify(t, sid, keyRes.GetPublicKey())
	}

	// At most one commit at a time, and no queue: a queue would give a commit per interval.
	spans := commitSpans(t, commitLog)
	for i := 1; i < len(spans); i++ {
		if spans[i].start.Before(spans[i-1].end) {
			t.Fatalf("commit %d started before commit %d ended", i+1, i)
		}
	}
	intervals := int(spoken / (500 * time.Millisecond))
	maxCommits := int(spoken/slowCommit) + 2
	if len(spans) > maxCommits {
		t.Errorf("%d commits for %s of speech with commits of %s: writes queued up (at most %d)", len(spans), spoken.Round(time.Second), slowCommit, maxCommits)
	}
	if len(commits) < 2 {
		t.Errorf("want at least one write while the session ran and one at the end, got %d", len(commits))
	}
	qs(t, "qs-archive-no-backpressure", "commit=%s utterances=%d intervals=%d commits=%d command %s stream %s target=max<%s",
		slowCommit, len(sent), intervals, len(spans), command.summary(), toStream.summary(), ms(targetTranscriptToLog))
	if assertTiming {
		if toStream.max() >= targetTranscriptToLog || command.max() >= targetTranscriptToLog {
			t.Errorf("a slow archive delays the session: command max %s, stream max %s, target < %s",
				ms(command.max()), ms(toStream.max()), ms(targetTranscriptToLog))
		}
	}
}

// recordCommit is how long every commit takes in TestArchiveRecordsBeforeSessionWrites.
const recordCommit = 2 * time.Second

// waitCommitRunning waits until a commit of the given kind runs, and returns it.
func waitCommitRunning(t *testing.T, logFile string, session bool) commitSpan {
	t.Helper()
	deadline := time.Now().Add(10 * recordCommit)
	for {
		if spans := commitSpans(t, logFile); len(spans) > 0 {
			if last := spans[len(spans)-1]; last.end.IsZero() && last.session == session {
				return last
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for a running commit (session write %t)", session)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// dec-records-synchronous: records go before waiting session writes. Every commit takes 2 s, the
// interval is 100 ms and a participant speaks every 50 ms, so a session write always waits. In
// each round a decision is recorded while a session write runs, and a note while the decision is
// committed. Neither waits for a session write that started after the call: the decision waits
// only for the session write in progress, the note for no session write at all. Session writes
// still happen between the rounds, and the last write has the whole session. The latency is
// asserted only with the build tag performance.
func TestArchiveRecordsBeforeSessionWrites(t *testing.T) {
	slow(t)
	gitDir, commitLog := slowGit(t, recordCommit)
	srv := contracttest.StartHub(t, []string{"PATH=" + gitDir + string(os.PathListSeparator) + os.Getenv("PATH")},
		"-archive-interval", "100ms")
	ada := srv.Participant(t, "ada", "Ada", identityv1.ActorKind_ACTOR_KIND_HUMAN)
	sid := "records-first-" + coreapi.NewCommandID()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := ada.Commands.Join(ctx, &sessioncommandsv1.JoinRequest{SessionId: sid, CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatal(err)
	}

	// Continuous session traffic until the rounds are over.
	speak, stopSpeaking := context.WithCancel(ctx)
	spoke := make(chan error, 1)
	go func() {
		for i := 0; speak.Err() == nil; i++ {
			if _, err := ada.Commands.SubmitUtterance(ctx, &sessioncommandsv1.SubmitUtteranceRequest{SessionId: sid,
				CommandId: coreapi.NewCommandID(), ParticipantId: "ada", Text: fmt.Sprintf("Sentence %d.", i),
				Modality: sessionstreamv1.Modality_MODALITY_TEXT}); err != nil {
				spoke <- fmt.Errorf("utterance %d: %w", i, err)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		spoke <- nil
	}()

	type call struct {
		name  string
		start time.Time
		took  time.Duration
	}
	const rounds = 3
	calls := make([]call, 0, 2*rounds) // no reallocation: the goroutines write through a pointer
	record := func(name string, do func() error) <-chan error {
		calls = append(calls, call{name: name, start: time.Now()})
		c := &calls[len(calls)-1]
		done := make(chan error, 1)
		go func() {
			err := do()
			c.took = time.Since(c.start)
			done <- err
		}()
		return done
	}
	for r := range rounds {
		waitCommitRunning(t, commitLog, true)
		decided := record(fmt.Sprintf("decision %d", r+1), func() error {
			_, err := ada.Decision.RecordDecision(ctx, &decisionv1.RecordDecisionRequest{CommandId: coreapi.NewCommandID(),
				Text: fmt.Sprintf("Decision %d.", r+1), AuthorParticipantId: "ada", SessionId: sid})
			return err
		})
		waitCommitRunning(t, commitLog, false)
		noted := record(fmt.Sprintf("note %d", r+1), func() error {
			_, err := ada.Note.AddNote(ctx, &notev1.AddNoteRequest{CommandId: coreapi.NewCommandID(),
				Text: fmt.Sprintf("Note %d.", r+1), AuthorParticipantId: "ada", SessionId: sid})
			return err
		})
		for _, done := range []<-chan error{decided, noted} {
			if err := <-done; err != nil {
				t.Fatalf("round %d: %v", r+1, err)
			}
		}
	}
	stopSpeaking()
	if err := <-spoke; err != nil {
		t.Fatal(err)
	}
	if _, err := ada.Commands.EndSession(ctx, &sessioncommandsv1.EndSessionRequest{SessionId: sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: "ada"}); err != nil {
		t.Fatal(err)
	}

	// Session writes still happen: the last write has the whole session.
	repo := srv.ArchiveDir()
	last := contracttest.WaitArchived(t, repo, sid, 10*recordCommit, (*contracttest.ArchivedSession).Ended)
	evs, err := ada.Stream.GetEvents(ctx, &sessionstreamv1.GetEventsRequest{SessionId: sid, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Events) != len(evs.GetEvents()) {
		t.Fatalf("archive has %d events, the session %d", len(last.Events), len(evs.GetEvents()))
	}

	spans := commitSpans(t, commitLog)
	var records, sessions []commitSpan
	for i, s := range spans {
		if i > 0 && s.start.Before(spans[i-1].end) {
			t.Fatalf("commit %d started before commit %d ended", i+1, i)
		}
		if s.session {
			sessions = append(sessions, s)
		} else {
			records = append(records, s)
		}
	}
	if len(records) != len(calls) {
		t.Fatalf("%d record commits for %d record calls", len(records), len(calls))
	}
	// The records are committed in the order of the calls: each call starts after the commit of
	// the one before has started.
	var took latencies
	for i, c := range calls {
		var waited []string
		started := 0
		for _, s := range sessions {
			if s.start.Before(records[i].start) && s.end.After(c.start) {
				waited = append(waited, fmt.Sprintf("%s-%s", s.start.Format("15:04:05.000"), s.end.Format("15:04:05.000")))
				if s.start.After(c.start) {
					started++
				}
			}
		}
		if started > 0 || len(waited) > 1 {
			t.Errorf("%s (called %s) waited for the session writes %v: want at most the one in progress",
				c.name, c.start.Format("15:04:05.000"), waited)
		}
		if i%2 == 1 && len(waited) > 0 {
			t.Errorf("%s was called while a record was committed, but waited for the session writes %v", c.name, waited)
		}
		took = append(took, c.took)
	}
	// Session writes ran between the rounds, not only at the end.
	between := 0
	for _, s := range sessions {
		if s.start.After(records[0].start) && s.end.Before(records[len(records)-1].start) {
			between++
		}
	}
	if between < rounds-1 {
		t.Errorf("%d session writes between the records of %d rounds, want at least %d", between, rounds, rounds-1)
	}
	t.Logf("commit=%s records %s, session writes %d (%d between the rounds)", recordCommit, took.summary(), len(sessions), between)
	if assertTiming {
		// One session write in progress or the record before, and the own commit.
		if limit := 2*recordCommit + 500*time.Millisecond; took.max() >= limit {
			t.Errorf("a record took %s, want < %s (at most one other commit and its own)", ms(took.max()), ms(limit))
		}
	}
}
