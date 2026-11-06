// Command office is the spike of the participant client (bb-cli).
//
//	office meeting join <session> [--name N] [--kind human|agent] [--wav FILE | --mic]
//
// It joins the session, starts the log replicator and prints the log path on stdout.
// With --wav or --mic it also runs the ears. Status lines go to stderr.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"

	officev1 "github.com/mrsimpson/agentic-office/gen/office/v1"
	"github.com/mrsimpson/agentic-office/internal/client"
	"github.com/mrsimpson/agentic-office/internal/ears"
	"github.com/mrsimpson/agentic-office/internal/replicator"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	if len(os.Args) < 4 || os.Args[1] != "meeting" || os.Args[2] != "join" {
		fmt.Fprintln(os.Stderr, "usage: office meeting join <session> [flags]")
		os.Exit(2)
	}
	session := os.Args[3]
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	server := fs.String("server", env("OFFICE_SERVER", "http://127.0.0.1:8740"), "office server URL")
	logDir := fs.String("log-dir", env("OFFICE_LOG_DIR", "/tmp/office"), "directory for the local session log")
	name := fs.String("name", env("USER", "participant"), "display name")
	kind := fs.String("kind", "", "human or agent (default: human with --wav/--mic, else agent)")
	wav := fs.String("wav", "", "run the ears on a WAV file")
	mic := fs.Bool("mic", false, "run the ears on the microphone (arecord)")
	fast := fs.Bool("fast", false, "with --wav: do not pace the audio in real time")
	exitAfter := fs.Bool("exit-after-audio", false, "with --wav: stop after all utterances are sent")
	model := fs.String("model", env("OFFICE_WHISPER_MODEL", "/srv/localai/models/whisper/ggml-small.bin"), "whisper ggml model")
	lang := fs.String("lang", "de", "spoken language")
	threads := fs.Uint("threads", 8, "whisper threads")
	threshold := fs.Float64("vad-threshold-db", -40, "VAD energy threshold in dBFS")
	endSilence := fs.Duration("end-silence", 500*time.Millisecond, "silence that ends an utterance")
	fs.Parse(os.Args[4:])
	if err := run(session, *server, *logDir, *name, *kind, *wav, *mic, *fast, *exitAfter, *model, *lang, *threads, *threshold, *endSilence); err != nil {
		log.Fatal(err)
	}
}

// state keeps the join command ID, so a restarted client joins as the same participant.
type state struct {
	JoinCommandID string `json:"join_command_id"`
}

func loadState(path string) state {
	var s state
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.JoinCommandID == "" {
		s.JoinCommandID = uuid.Must(uuid.NewV7()).String()
		b, _ := json.Marshal(s)
		_ = os.WriteFile(path, b, 0o600)
	}
	return s
}

func run(session, server, logDir, name, kind, wav string, mic, fast, exitAfter bool, model, lang string, threads uint, threshold float64, endSilence time.Duration) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hasEars := wav != "" || mic
	if kind == "" {
		kind = map[bool]string{true: "human", false: "agent"}[hasEars]
	}
	pk := officev1.ParticipantKind_PARTICIPANT_KIND_AGENT
	if kind == "human" {
		pk = officev1.ParticipantKind_PARTICIPANT_KIND_HUMAN
	}
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return err
	}
	st := loadState(filepath.Join(logDir, session+"."+name+".state.json"))

	c := client.New(server)
	var pid string
	for {
		res, err := c.Join(ctx, &officev1.JoinRequest{SessionId: session, CommandId: st.JoinCommandID, DisplayName: name, Kind: pk})
		if err == nil {
			pid = res.GetParticipantId()
			break
		}
		if ctx.Err() != nil {
			return err
		}
		log.Printf("join failed (%v), retry", err)
		time.Sleep(500 * time.Millisecond)
	}

	rep := &replicator.Replicator{Client: c, Session: session, Path: replicator.Path(logDir, session)}
	if err := rep.Open(); err != nil {
		return err
	}
	fmt.Println(rep.Path) // the agent reads this line
	log.Printf("joined %s as %s (%s), participant %s, cursor %d", session, name, kind, pid, rep.Cursor())
	repDone := make(chan error, 1)
	go func() { repDone <- rep.Run(ctx) }()

	if !hasEars {
		<-ctx.Done()
		return <-repDone
	}

	tr, err := ears.NewTranscriber(model, lang, threads)
	if err != nil {
		return fmt.Errorf("load whisper model: %w", err)
	}
	defer tr.Close()
	var frames <-chan ears.Frame
	if mic {
		if frames, err = ears.MicSource(ctx); err != nil {
			return err
		}
	} else {
		samples, err := ears.ReadWAV(wav)
		if err != nil {
			return err
		}
		frames = ears.WAVSource(ctx, samples, !fast)
	}
	e := &ears.Ears{
		Client: c, Session: session, ParticipantID: pid, Transcriber: tr, Status: os.Stderr,
		VAD: ears.VAD{ThresholdDB: threshold, EndSilence: endSilence, PreRoll: 300 * time.Millisecond,
			MinSpeech: 200 * time.Millisecond, MaxSpeech: 20 * time.Second},
	}
	if err := e.Run(ctx, frames); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Printf("ears: audio done")
	if exitAfter {
		stop()
		return <-repDone
	}
	<-ctx.Done()
	return <-repDone
}
