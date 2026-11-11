package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"sync"
	"time"

	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"

	"github.com/mrsimpson/agentic-office/client/core"
	"github.com/mrsimpson/agentic-office/client/replicator"
)

// errEarsNotImplemented is returned by join --mic and --wav in a build without the ears.
var errEarsNotImplemented = errors.New("--mic and --wav need the ears, which are not built in; " +
	"join without them and run \"office meeting view\" for the gateway view")

// EarsOptions tell the ears what to listen to. Empty fields mean the environment or the default
// of the ears.
type EarsOptions struct {
	// Mic is true for the default microphone.
	Mic bool
	// WAV is a WAV file to play instead of the microphone.
	WAV string
	// Model is a name from the model catalogue or the path of a Whisper model. VADModel is the
	// path of the VAD model.
	Model, VADModel string
	// Fast chooses the fast Whisper model when Model is empty.
	Fast bool
	// Language is the spoken language, a code or a name, for example "de" or "german".
	Language string
	// Threads is the number of Whisper threads.
	Threads int
	// EndSilence is the silence that ends a sentence.
	EndSilence time.Duration
	// Vocabulary is a file with terms for Whisper's prompt. Empty means .office/vocabulary.txt
	// in the current directory, if it is there.
	Vocabulary string
	// Context adds the previous sentence to Whisper's prompt.
	Context bool
	// Names returns the display names of the participants of the session, for Whisper's prompt.
	// Set by the CLI from the local log.
	Names func() []string
	// Report prints progress and measurements of the ears: text is the short form for the
	// terminal, fields the form for --json. Set by the CLI.
	Report func(text string, fields any)
}

// CheckEars is called before join --mic or --wav. It checks the options (models, WAV file,
// recorder), so that a join does not mark the participant as captured when the ears cannot
// start. It downloads a missing default model and reports each step to progress. Nil means no
// check.
var CheckEars func(ctx context.Context, opts *EarsOptions, progress func(string)) error

// ModelOptions choose a speech model as join does: a name from the catalogue or a file, else
// --fast, else the best model for the language.
type ModelOptions struct {
	Name, Language string
	Fast           bool
}

// DownloadModels is the hook for "office models download": it downloads the chosen speech model
// and the VAD model if they are missing and reports each step to progress. Nil means the ears are
// not built in.
var DownloadModels func(ctx context.Context, o ModelOptions, progress func(string)) error

// ModelList is the result of "office models list".
type ModelList struct {
	// Models are the models of the catalogue, as JSON objects with name, file, sizeBytes,
	// languages, about, downloaded and selected.
	Models []ModelEntry `json:"models"`
	// Selected is the speech model that join would use with the same options: a name, or a file.
	Selected string `json:"selected"`
	// Language is the Whisper code of the language.
	Language string `json:"language"`
	// Note says why another model than the best one for the language is selected.
	Note string `json:"note,omitempty"`
}

// ModelEntry is one model of the catalogue.
type ModelEntry struct {
	Name       string   `json:"name"`
	File       string   `json:"file"`
	SizeBytes  int64    `json:"sizeBytes"`
	Languages  []string `json:"languages,omitempty"`
	About      string   `json:"about"`
	Downloaded bool     `json:"downloaded"`
	Selected   bool     `json:"selected"`
}

// ListModels is the hook for "office models list". Nil means the ears are not built in.
var ListModels func(o ModelOptions) (ModelList, error)

// StartEars is the hook for the ears (client/ears). It runs until ctx ends or the audio is over.
// It submits final utterances with c.SubmitUtterance under a command ID it creates, and reports
// the local status of each utterance (partial, final) to status before it sends it. It must not
// transcribe while paused() is true. Nil means the ears are not built in.
var StartEars func(ctx context.Context, c *core.Client, sessionID string, opts EarsOptions,
	status func(cmdID, text string, s core.Status), paused func() bool) error

func cmdJoin(e *env, fs *flag.FlagSet) func([]string) error {
	name := fs.String("name", "", "display name in this session (default: the registered name)")
	fg := fs.Bool("foreground", false, "stay in the foreground; the replicator stops when this process ends")
	mic := fs.Bool("mic", false, "transcribe the microphone and show the gateway view")
	wav := fs.String("wav", "", "like --mic, but play a WAV file in real time")
	var eo EarsOptions
	fs.StringVar(&eo.Model, "model", "", "Whisper model for --mic/--wav: a name of \"office models list\" or a file (env OFFICE_WHISPER_MODEL)")
	fs.BoolVar(&eo.Fast, "fast", false, "use the fast Whisper model small instead of the best one for the language")
	fs.StringVar(&eo.VADModel, "vad-model", "", "Silero VAD model for --mic/--wav (env OFFICE_VAD_MODEL)")
	fs.StringVar(&eo.Language, "language", "", "spoken language for --mic/--wav, a code or a name such as de, german, en, english "+
		"(env OFFICE_WHISPER_LANGUAGE, default de)")
	fs.IntVar(&eo.Threads, "threads", 0, "Whisper threads for --mic/--wav (env OFFICE_WHISPER_THREADS)")
	fs.DurationVar(&eo.EndSilence, "end-silence", 0, "silence that ends a sentence (env OFFICE_EARS_END_SILENCE, default 500ms)")
	fs.StringVar(&eo.Vocabulary, "vocabulary", "", "file with terms for Whisper, one per line (default .office/vocabulary.txt, if it is there)")
	fs.BoolVar(&eo.Context, "context", false, "give Whisper the previous sentence as context")
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office meeting join <session> [--name N] [--foreground] [--mic | --wav FILE] " +
				"[--language L] [--fast] [--model NAME|FILE] [--vocabulary FILE] [--context] [--vad-model F] [--threads N] " +
				"[--end-silence D]")
		}
		sid := args[0]
		ears := *mic || *wav != ""
		if *mic && *wav != "" {
			return usagef("use --mic or --wav, not both")
		}
		if ears && StartEars == nil {
			return errEarsNotImplemented
		}
		eo.Mic, eo.WAV = *mic, *wav
		if ears && CheckEars != nil {
			if err := CheckEars(e.ctx, &eo, e.progress); err != nil {
				return err
			}
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		j, err := c.Join(e.ctx, sid, core.JoinOptions{DisplayName: *name, Captured: ears})
		if err != nil {
			return err
		}
		rep, err := startReplicator(e, j, *fg || ears)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"sessionId": j.SessionID, "participantId": j.ParticipantID, "logPath": j.Paths.Log,
				"replicatorPid": rep.pid, "event": eventJSON(j.Result.GetEvent()), "duplicate": j.Result.GetDuplicate()})
		} else {
			fmt.Fprintln(e.stdout, j.Paths.Log)
		}
		if !*fg && !ears {
			return nil
		}
		if ears {
			return runEarsGateway(e, c, j, eo, rep)
		}
		return rep.wait(e.ctx)
	}
}

// runEarsGateway runs the ears and the gateway view in the foreground (join --mic or --wav).
// The view stays when the audio of a WAV file is over; q or the end of the session stops it.
// If the ears fail, join stops with their error.
func runEarsGateway(e *env, c *core.Client, j *core.Joined, opts EarsOptions, rep *replicatorProc) error {
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	local := make(chan localStatus, 64)
	gw := &gatewayView{e: e, c: c, sid: j.SessionID, pid: j.ParticipantID, log: j.Paths.Log, local: local}
	opts.Report = gw.earsReport
	opts.Names = followNames(ctx, j.Paths.Log)
	earsErr := make(chan error, 1)
	go func() {
		err := StartEars(ctx, c, j.SessionID, opts, func(cmdID, text string, s core.Status) {
			select {
			case local <- localStatus{cmdID, text, s}:
			case <-ctx.Done():
			}
		}, gw.paused.Load)
		if err != nil && ctx.Err() == nil {
			earsErr <- fmt.Errorf("ears: %w", err)
			cancel()
		}
	}()
	err := gw.run(ctx)
	cancel()
	rep.stop()
	select {
	case eerr := <-earsErr:
		return eerr
	default:
	}
	return err
}

func cmdLogPath(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		c, sid, pid, err := e.joined()
		if err != nil {
			return err
		}
		p, err := c.Paths(sid, pid)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"sessionId": sid, "participantId": pid, "logPath": p.Log,
				"replicatorRunning": replicator.Running(p.Log)})
		} else {
			fmt.Fprintln(e.stdout, p.Log)
		}
		return nil
	}
}

// joined returns a client, the session and the participant.
func (e *env) joined() (*core.Client, string, string, error) {
	c, err := e.client()
	if err != nil {
		return nil, "", "", err
	}
	sid, err := e.sessionID(c)
	if err != nil {
		return nil, "", "", err
	}
	pid, err := c.ParticipantID(e.ctx)
	if err != nil {
		return nil, "", "", err
	}
	return c, sid, pid, nil
}

func cmdLeave(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		c, sid, pid, err := e.joined()
		if err != nil {
			return err
		}
		res, err := c.Leave(e.ctx, sid)
		if err != nil {
			return err
		}
		// The replicator stops after it wrote participant.left. Wait for it, so that a join right
		// after this starts a new one.
		if p, err := c.Paths(sid, pid); err == nil {
			deadline := time.Now().Add(5 * time.Second)
			for replicator.Running(p.Log) && time.Now().Before(deadline) && e.ctx.Err() == nil {
				time.Sleep(20 * time.Millisecond)
			}
		}
		return e.printResult(res)
	}
}

func cmdView(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		c, sid, pid, err := e.joined()
		if err != nil {
			return err
		}
		p, err := c.Paths(sid, pid)
		if err != nil {
			return err
		}
		gw := &gatewayView{e: e, c: c, sid: sid, pid: pid, log: p.Log}
		return gw.run(e.ctx)
	}
}

// cmdModelsDownload downloads the models of the ears ahead of time, for example before the first
// meeting or while building an image. join --mic and --wav also do it when a model is missing.
func cmdModelsDownload(e *env, fs *flag.FlagSet) func([]string) error {
	var o ModelOptions
	modelFlags(fs, &o)
	return func(args []string) error {
		if len(args) > 1 {
			return usagef("usage: office models download [--language L] [--fast] [<name>]")
		}
		if len(args) == 1 {
			o.Name = args[0]
		}
		if DownloadModels == nil {
			return errEarsNotImplemented
		}
		return DownloadModels(e.ctx, o, e.progress)
	}
}

// cmdModelsList shows the model catalogue, what is downloaded, and which speech model join would
// use with the same options.
func cmdModelsList(e *env, fs *flag.FlagSet) func([]string) error {
	var o ModelOptions
	modelFlags(fs, &o)
	fs.StringVar(&o.Name, "model", "", "a name or a file, as for join (env OFFICE_WHISPER_MODEL)")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if ListModels == nil {
			return errEarsNotImplemented
		}
		l, err := ListModels(o)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(l)
			return nil
		}
		for _, m := range l.Models {
			mark, have := " ", "not downloaded"
			if m.Selected {
				mark = "*"
			}
			if m.Downloaded {
				have = "downloaded"
			}
			langs := "all languages"
			if len(m.Languages) > 0 {
				langs = strings.Join(m.Languages, ", ")
			}
			fmt.Fprintf(e.stdout, "%s %-22s %5d MB  %-14s  %-14s  %s\n", mark, m.Name, (m.SizeBytes+500_000)/1_000_000, langs, have, m.About)
		}
		fmt.Fprintf(e.stdout, "selected for language %s: %s\n", l.Language, l.Selected)
		if l.Note != "" {
			fmt.Fprintln(e.stdout, l.Note)
		}
		return nil
	}
}

func modelFlags(fs *flag.FlagSet, o *ModelOptions) {
	fs.StringVar(&o.Language, "language", "", "spoken language, a code or a name (env OFFICE_WHISPER_LANGUAGE, default de)")
	fs.BoolVar(&o.Fast, "fast", false, "the fast model small instead of the best one for the language")
}

// followNames follows the local log and returns a function with the display names of the
// participants who joined the session so far, in the order they joined, each once.
func followNames(ctx context.Context, log string) func() []string {
	var mu sync.Mutex
	var names []string
	seen := map[string]bool{}
	go core.FollowLog(ctx, log, true, func(ev *sessionv1.Event) error {
		if n := ev.GetParticipantJoined().GetDisplayName(); n != "" {
			mu.Lock()
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
			mu.Unlock()
		}
		return nil
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), names...)
	}
}
