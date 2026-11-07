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

	"github.com/mrsimpson/agentic-office/client/core/ids"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// slowCommit is how long every commit of the archive takes in qs-archive-no-backpressure.
const slowCommit = 5 * time.Second

// slowGit writes a git wrapper that makes every "git commit" take slowCommit and logs when each
// commit starts and ends. It returns the directory to put first in PATH and the log file.
func slowGit(t *testing.T) (string, string) {
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
if [ "$1" = commit ]; then
  echo "start $(date +%%s%%N)" >> %[1]q
  sleep %[3]d
  %[2]q "$@"; rc=$?
  echo "end $(date +%%s%%N)" >> %[1]q
  exit $rc
fi
exec %[2]q "$@"
`, logFile, real, int(slowCommit.Seconds()))
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, logFile
}

// commitSpans reads the start and end times of the commits from the log of slowGit.
func commitSpans(t *testing.T, logFile string) [][2]time.Time {
	t.Helper()
	b, err := os.ReadFile(logFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	var spans [][2]time.Time
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		ns, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			t.Fatalf("commit log: %q", line)
		}
		at := time.Unix(0, ns)
		switch f[0] {
		case "start":
			spans = append(spans, [2]time.Time{at})
		case "end":
			if len(spans) == 0 || !spans[len(spans)-1][1].IsZero() {
				t.Fatalf("two commits at the same time (an end without its start):\n%s", b)
			}
			spans[len(spans)-1][1] = at
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
	gitDir, commitLog := slowGit(t)
	duration := qsDuration(t, "QS_ARCHIVE_DURATION", 12*time.Second)
	srv := contracttest.StartHub(t, []string{"PATH=" + gitDir + string(os.PathListSeparator) + os.Getenv("PATH")},
		"-archive-interval", "500ms")
	ada := srv.Participant(t, "ada", "Ada", identityv1.ActorKind_ACTOR_KIND_HUMAN)
	bot := srv.Participant(t, "bot", "Bot", identityv1.ActorKind_ACTOR_KIND_AGENT)
	sid := "archive-" + ids.NewCommandID()
	ctx, cancel := context.WithTimeout(context.Background(), duration+2*time.Minute)
	defer cancel()
	if _, err := ada.Session.Join(ctx, &sessionv1.JoinRequest{SessionId: sid, CommandId: ids.NewCommandID()}); err != nil {
		t.Fatal(err)
	}

	// The bot follows the stream and notes when each event arrives.
	var mu sync.Mutex
	arrived := map[uint32]time.Time{}
	stream, err := bot.Session.Subscribe(ctx, &sessionv1.SubscribeRequest{SessionId: sid})
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
		res, err := ada.Session.SubmitUtterance(ctx, &sessionv1.SubmitUtteranceRequest{SessionId: sid,
			CommandId: ids.NewCommandID(), ParticipantId: "ada", Text: fmt.Sprintf("Sentence %d.", i),
			Modality: sessionv1.Modality_MODALITY_TEXT})
		if err != nil {
			t.Fatalf("utterance %d: %v", i, err)
		}
		command = append(command, time.Since(t0))
		sent[res.GetResult().GetEvent().GetId()] = t0
		time.Sleep(100 * time.Millisecond)
	}
	spoken := time.Since(start)
	if _, err := ada.Session.EndSession(ctx, &sessionv1.EndSessionRequest{SessionId: sid, CommandId: ids.NewCommandID(),
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
	evs, err := ada.Session.GetEvents(ctx, &sessionv1.GetEventsRequest{SessionId: sid, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Events) != len(evs.GetEvents()) {
		t.Fatalf("archive has %d events, the session %d", len(last.Events), len(evs.GetEvents()))
	}
	keyRes, err := ada.Session.GetHubKey(ctx, &sessionv1.GetHubKeyRequest{})
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
		if spans[i][0].Before(spans[i-1][1]) {
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
