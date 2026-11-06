//go:build e2e

// Package e2e runs the spike end to end with real processes: office-server, an agent
// participant (replicator only) and a human participant whose ears read a WAV file.
//
//	make e2e   (optional: OFFICE_TEST_WAV, OFFICE_WHISPER_MODEL)
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	officev1 "github.com/mrsimpson/agentic-office/gen/office/v1"
	"github.com/mrsimpson/agentic-office/internal/client"
)

var binDir = func() string {
	if d := os.Getenv("OFFICE_BIN_DIR"); d != "" {
		return d
	}
	d, _ := filepath.Abs("../bin")
	return d
}()

// proc is a running child process with its stderr collected.
type proc struct {
	cmd    *exec.Cmd
	stdout *bufio.Reader
	mu     sync.Mutex
	stderr bytes.Buffer
	done   chan struct{}
}

func start(t *testing.T, env []string, name string, args ...string) *proc {
	t.Helper()
	cmd := exec.Command(filepath.Join(binDir, name), args...)
	cmd.Env = append(os.Environ(), env...)
	out, _ := cmd.StdoutPipe()
	errp, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &proc{cmd: cmd, stdout: bufio.NewReader(out), done: make(chan struct{})}
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := errp.Read(b)
			p.mu.Lock()
			p.stderr.Write(b[:n])
			p.mu.Unlock()
			if err != nil {
				break
			}
		}
		cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() { p.kill() })
	return p
}

func (p *proc) kill() {
	_ = p.cmd.Process.Signal(syscall.SIGKILL)
	<-p.done
}

func (p *proc) errText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderr.String()
}

// startServer starts office-server on a free port and returns its URL.
func startServer(t *testing.T) string {
	p := start(t, nil, "office-server", "-addr", "127.0.0.1:0")
	re := regexp.MustCompile(`listening on (http://\S+)`)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := re.FindStringSubmatch(p.errText()); m != nil {
			return m[1]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not start: %s", p.errText())
	return ""
}

// joinAgent starts "office meeting join" without ears and returns the process and the log path.
func joinAgent(t *testing.T, url, session, dir string) (*proc, string) {
	p := start(t, []string{"OFFICE_SERVER=" + url, "OFFICE_SPIKE_METRICS=1"},
		"office", "meeting", "join", session, "--name", "agent", "--log-dir", dir)
	line, err := p.stdout.ReadString('\n')
	if err != nil {
		t.Fatalf("no log path: %v\n%s", err, p.errText())
	}
	return p, strings.TrimSpace(line)
}

func readLog(t *testing.T, path string) []*officev1.Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var evs []*officev1.Event
	for _, l := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
		if len(l) == 0 {
			continue
		}
		ev := &officev1.Event{}
		if err := protojson.Unmarshal(l, ev); err != nil {
			t.Fatalf("bad line %q: %v", l, err)
		}
		evs = append(evs, ev)
	}
	return evs
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

// replicaWrites reads the METRIC lines of a replicator: event ID -> write time.
func replicaWrites(stderr string) map[uint64]time.Time {
	m := map[uint64]time.Time{}
	for _, l := range strings.Split(stderr, "\n") {
		if !strings.HasPrefix(l, "METRIC ") {
			continue
		}
		var r struct {
			Metric  string `json:"metric"`
			EventID uint64 `json:"event_id"`
			At      int64  `json:"at_unix_nano"`
		}
		if json.Unmarshal([]byte(l[7:]), &r) == nil && r.Metric == "replica_write" {
			m[r.EventID] = time.Unix(0, r.At)
		}
	}
	return m
}

func checkContiguous(t *testing.T, evs []*officev1.Event) {
	t.Helper()
	seen := map[string]bool{}
	for i, ev := range evs {
		if ev.GetId() != uint64(i+1) {
			t.Fatalf("line %d has id %d: lost or duplicate event", i+1, ev.GetId())
		}
		if seen[ev.GetCommandId()] {
			t.Fatalf("command %s appears twice", ev.GetCommandId())
		}
		seen[ev.GetCommandId()] = true
	}
}

type stats struct{ p50, p95, max time.Duration }

func summarize(ds []time.Duration) stats {
	slices.Sort(ds)
	at := func(p float64) time.Duration { return ds[min(len(ds)-1, int(p*float64(len(ds))))] }
	return stats{at(0.5), at(0.95), ds[len(ds)-1]}
}

func (s stats) String() string { return fmt.Sprintf("p50=%v p95=%v max=%v", s.p50, s.p95, s.max) }

// TestVoiceToAgentLog feeds German speech through the ears of a human participant and checks the
// agent's local log.
func TestVoiceToAgentLog(t *testing.T) {
	wav := os.Getenv("OFFICE_TEST_WAV")
	if wav == "" {
		wav, _ = filepath.Abs("testdata/german-3-sentences.wav")
	}
	url := startServer(t)
	tmp := t.TempDir()
	session := "voice-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	agent, path := joinAgent(t, url, session, filepath.Join(tmp, "agent"))
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("log file %s: mode %v, err %v", path, fi.Mode().Perm(), err)
	}

	human := start(t, []string{"OFFICE_SERVER=" + url, "OFFICE_SPIKE_METRICS=1"},
		"office", "meeting", "join", session, "--name", "alice", "--log-dir", filepath.Join(tmp, "alice"),
		"--wav", wav, "--exit-after-audio")
	select {
	case <-human.done:
	case <-time.After(2 * time.Minute):
		t.Fatal("ears did not finish")
	}
	if human.cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("ears failed:\n%s", human.errText())
	}
	for _, l := range strings.Split(human.errText(), "\n") {
		if strings.HasPrefix(l, "METRIC ") && !strings.Contains(l, "replica_write") {
			t.Log(l[7:])
		}
	}

	var utts []*officev1.Event
	waitFor(t, 5*time.Second, "3 utterances in agent log", func() bool {
		utts = utts[:0]
		for _, ev := range readLog(t, path) {
			if ev.GetType() == "utterance.final" {
				utts = append(utts, ev)
			}
		}
		return len(utts) >= 3
	})
	checkContiguous(t, readLog(t, path))

	var text []string
	writes := replicaWrites(agent.errText())
	for _, ev := range utts {
		u := ev.GetUtteranceFinal()
		text = append(text, u.GetText())
		toLog := ev.GetTime().AsTime().Sub(u.GetTranscribedAt().AsTime())
		toReplica := writes[ev.GetId()].Sub(u.GetTranscribedAt().AsTime())
		t.Logf("#%d %q: transcribed->server log %v, transcribed->agent file %v", ev.GetId(), u.GetText(), toLog, toReplica)
		if toLog > 200*time.Millisecond || toReplica > 200*time.Millisecond {
			t.Errorf("qs-transcript-to-log missed: %v / %v", toLog, toReplica)
		}
		if u.GetModality() != officev1.Modality_MODALITY_VOICE {
			t.Errorf("modality %v", u.GetModality())
		}
	}
	all := strings.ToLower(strings.Join(text, " "))
	for _, w := range []string{"guten morgen", "besprechen", "büro", "zahlen", "freitag"} {
		if !strings.Contains(all, w) {
			t.Errorf("transcript %q misses %q", all, w)
		}
	}
	if len(utts) != 3 {
		t.Errorf("want 3 utterances (VAD splits at pauses), got %d: %q", len(utts), text)
	}
}

// TestResumeAndIdempotency kills and restarts the agent's replicator while utterances arrive.
// The local file must end up with every event exactly once.
func TestResumeAndIdempotency(t *testing.T) {
	url := startServer(t)
	tmp := t.TempDir()
	session := "resume-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	agentDir := filepath.Join(tmp, "agent")
	agent, path := joinAgent(t, url, session, agentDir)

	c := client.New(url)
	ctx := context.Background()
	jr, err := c.Join(ctx, &officev1.JoinRequest{SessionId: session, CommandId: uuid.NewString(), DisplayName: "writer", Kind: officev1.ParticipantKind_PARTICIPANT_KIND_HUMAN})
	if err != nil {
		t.Fatal(err)
	}
	const n = 300
	submitDone := make(chan struct{})
	dups := 0
	go func() {
		defer close(submitDone)
		for i := 0; i < n; i++ {
			req := &officev1.SubmitUtteranceRequest{SessionId: session, CommandId: uuid.Must(uuid.NewV7()).String(),
				ParticipantId: jr.GetParticipantId(), Text: fmt.Sprintf("Satz %d", i), Modality: officev1.Modality_MODALITY_TEXT,
				TranscribedAt: timestamppb.Now()}
			first, err := c.SubmitUtterance(ctx, req)
			if err != nil {
				t.Error(err)
				return
			}
			if i%10 == 0 { // the client repeats the command, as after a drop
				again, err := c.SubmitUtterance(ctx, req)
				if err != nil || !again.GetDuplicate() || again.GetEvent().GetId() != first.GetEvent().GetId() {
					t.Errorf("repeat of %s: dup=%v id=%d want %d err=%v", req.CommandId, again.GetDuplicate(), again.GetEvent().GetId(), first.GetEvent().GetId(), err)
				}
				dups++
			}
			time.Sleep(3 * time.Millisecond)
		}
	}()

	for k := 0; k < 3; k++ {
		time.Sleep(200 * time.Millisecond)
		agent.kill()
		if k == 1 { // simulate a crash in the middle of a write
			f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
			io.WriteString(f, `{"id":"99999","sessionId":"tor`)
			f.Close()
		}
		time.Sleep(100 * time.Millisecond)
		agent, _ = joinAgent(t, url, session, agentDir)
	}
	<-submitDone

	want := 2 + n // two joins (agent rejoins as the same participant) plus n utterances
	waitFor(t, 5*time.Second, fmt.Sprintf("%d lines in agent log", want), func() bool { return len(readLog(t, path)) >= want })
	time.Sleep(200 * time.Millisecond) // nothing more should arrive
	evs := readLog(t, path)
	checkContiguous(t, evs)
	if len(evs) != want {
		t.Fatalf("want %d events, got %d", want, len(evs))
	}
	t.Logf("%d events, %d repeated commands, 3 replicator kills (one with a torn line): no loss, no duplicates", len(evs), dups)
}

// TestLatencyDistribution measures qs-transcript-to-log for many utterances without disturbance.
func TestLatencyDistribution(t *testing.T) {
	url := startServer(t)
	session := "lat-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	agent, path := joinAgent(t, url, session, filepath.Join(t.TempDir(), "agent"))
	c := client.New(url)
	ctx := context.Background()
	jr, err := c.Join(ctx, &officev1.JoinRequest{SessionId: session, CommandId: uuid.NewString(), DisplayName: "writer"})
	if err != nil {
		t.Fatal(err)
	}
	const n = 500
	var ack []time.Duration
	for i := 0; i < n; i++ {
		at := time.Now()
		if _, err := c.SubmitUtterance(ctx, &officev1.SubmitUtteranceRequest{SessionId: session, CommandId: uuid.NewString(),
			ParticipantId: jr.GetParticipantId(), Text: "Hallo", TranscribedAt: timestamppb.New(at)}); err != nil {
			t.Fatal(err)
		}
		ack = append(ack, time.Since(at))
		time.Sleep(5 * time.Millisecond)
	}
	waitFor(t, 5*time.Second, "all lines", func() bool { return len(readLog(t, path)) >= n+2 })
	writes := replicaWrites(agent.errText())
	var toLog, toFile []time.Duration
	for _, ev := range readLog(t, path) {
		u := ev.GetUtteranceFinal()
		if u == nil {
			continue
		}
		toLog = append(toLog, ev.GetTime().AsTime().Sub(u.GetTranscribedAt().AsTime()))
		toFile = append(toFile, writes[ev.GetId()].Sub(u.GetTranscribedAt().AsTime()))
	}
	sl, sf := summarize(toLog), summarize(toFile)
	t.Logf("n=%d, all on localhost", n)
	t.Logf("transcribed -> server log:          %v", sl)
	t.Logf("transcribed -> submit acknowledged: %v", summarize(ack))
	t.Logf("transcribed -> agent local file:    %v", sf)
	if sl.p95 > 200*time.Millisecond || sf.p95 > 200*time.Millisecond {
		t.Errorf("p95 over 200 ms")
	}
}
