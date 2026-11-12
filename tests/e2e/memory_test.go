//go:build e2e

package e2e

// Quality scenario qs-session-memory (chapter 10, dec-session-eviction): ended sessions leave the
// memory of the hub. The test imports many large conversations, each a finished session, and
// waits until the hub reports that every one of them left memory. It then reads one of them, which
// the hub loads from the archive again. The test prints the resident memory of the hub before and
// after; the number depends on the garbage collector of the host, so only "make performance"
// compares it with the data that was imported. Behaviour is checked in every run.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	sessionimportv1 "github.com/mrsimpson/agentic-office/hub/session/importapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// residentKiB returns the resident memory of a process in KiB, or 0 where /proc is not there.
func residentKiB(pid int) int {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			f := strings.Fields(rest)
			if len(f) > 0 {
				n, _ := strconv.Atoi(f[0])
				return n
			}
		}
	}
	return 0
}

func ctx60(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func TestQualitySessionMemory(t *testing.T) {
	const sessions, utterances, textBytes = 60, 200, 4096
	srv := contracttest.StartHub(t, nil, "-archive-interval", "100ms")
	ada := srv.Participant(t, "ada", "Ada", identityv1.ActorKind_ACTOR_KIND_HUMAN)
	before := residentKiB(srv.Cmd.Process.Pid)

	text := strings.Repeat("Budget for the new office. ", textBytes/27)
	for i := 0; i < sessions; i++ {
		var utts []*sessionimportv1.ImportedUtterance
		for j := 0; j < utterances; j++ {
			utts = append(utts, &sessionimportv1.ImportedUtterance{Speaker: "ada", Text: text,
				Modality: sessionstreamv1.Modality_MODALITY_TEXT, Time: timestamppb.New(time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))})
		}
		_, err := ada.Import.ImportConversation(ctx60(t), &sessionimportv1.ImportConversationRequest{
			SessionId: fmt.Sprintf("import-%03d", i), CommandId: coreapi.NewCommandID(), ParticipantId: "ada",
			Conversation: &sessionimportv1.Conversation{Source: "test", Title: "Budget"},
			Participants: []*sessionimportv1.ImportedParticipant{{Key: "ada", DisplayName: "Ada",
				Kind: sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN}},
			Utterances: utts, Complete: true})
		if err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	for strings.Count(srv.Stderr(), "left memory") < sessions {
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d sessions left memory", strings.Count(srv.Stderr(), "left memory"), sessions)
		}
		time.Sleep(50 * time.Millisecond)
	}
	after := residentKiB(srv.Cmd.Process.Pid)

	// A session that left memory is read from the archive.
	res, err := ada.Stream.GetEvents(ctx60(t), &sessionstreamv1.GetEventsRequest{SessionId: "import-000", Limit: 5})
	if err != nil || len(res.GetEvents()) != 5 {
		t.Fatalf("read a session that left memory: %v, %v", res, err)
	}
	dataKiB := sessions * utterances * textBytes / 1024
	qs(t, "qs-session-memory", "sessions=%d data=%dKiB rss-before=%dKiB rss-after-all-left-memory=%dKiB", sessions, dataKiB, before, after)
	if assertTiming && before > 0 && after-before > dataKiB/2 {
		t.Errorf("the hub holds %d KiB more than at the start after %d KiB of ended sessions", after-before, dataKiB)
	}
}
