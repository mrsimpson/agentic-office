package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/mrsimpson/agentic-office/client/participant"
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
	// Model and VADModel are the paths of the Whisper and the VAD model.
	Model, VADModel string
	// Language is the spoken language, for example "de".
	Language string
	// Threads is the number of Whisper threads.
	Threads int
	// EndSilence is the silence that ends a sentence.
	EndSilence time.Duration
	// Report prints progress and measurements of the ears: text is the short form for the
	// terminal, fields the form for --json. Set by the CLI.
	Report func(text string, fields any)
}

// CheckEars is called before join --mic or --wav. It checks the options (models, WAV file,
// recorder), so that a join does not mark the participant as captured when the ears cannot
// start. Nil means no check.
var CheckEars func(opts *EarsOptions) error

// StartEars is the hook for the ears (client/ears). It runs until ctx ends or the audio is over.
// It submits final utterances with c.SubmitUtterance under a command ID it creates, and reports
// the local status of each utterance (partial, final) to status before it sends it. It must not
// transcribe while paused() is true. Nil means the ears are not built in.
var StartEars func(ctx context.Context, c *participant.Client, sessionID string, opts EarsOptions,
	status func(cmdID, text string, s participant.Status), paused func() bool) error

func cmdJoin(e *env, fs *flag.FlagSet) func([]string) error {
	name := fs.String("name", "", "display name in this session (default: the registered name)")
	fg := fs.Bool("foreground", false, "stay in the foreground; the replicator stops when this process ends")
	mic := fs.Bool("mic", false, "transcribe the microphone and show the gateway view")
	wav := fs.String("wav", "", "like --mic, but play a WAV file in real time")
	var eo EarsOptions
	fs.StringVar(&eo.Model, "model", "", "Whisper model for --mic/--wav (env OFFICE_WHISPER_MODEL)")
	fs.StringVar(&eo.VADModel, "vad-model", "", "Silero VAD model for --mic/--wav (env OFFICE_VAD_MODEL)")
	fs.StringVar(&eo.Language, "language", "", "spoken language for --mic/--wav (env OFFICE_WHISPER_LANGUAGE, default de)")
	fs.IntVar(&eo.Threads, "threads", 0, "Whisper threads for --mic/--wav (env OFFICE_WHISPER_THREADS)")
	fs.DurationVar(&eo.EndSilence, "end-silence", 0, "silence that ends a sentence (env OFFICE_EARS_END_SILENCE, default 500ms)")
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office meeting join <session> [--name N] [--foreground] [--mic | --wav FILE] " +
				"[--model F] [--vad-model F] [--language L] [--threads N] [--end-silence D]")
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
			if err := CheckEars(&eo); err != nil {
				return err
			}
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		j, err := c.Join(e.ctx, sid, participant.JoinOptions{DisplayName: *name, Captured: ears})
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
func runEarsGateway(e *env, c *participant.Client, j *participant.Joined, opts EarsOptions, rep *replicatorProc) error {
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	local := make(chan localStatus, 64)
	gw := &gatewayView{e: e, c: c, sid: j.SessionID, pid: j.ParticipantID, log: j.Paths.Log, local: local}
	opts.Report = gw.earsReport
	earsErr := make(chan error, 1)
	go func() {
		err := StartEars(ctx, c, j.SessionID, opts, func(cmdID, text string, s participant.Status) {
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
func (e *env) joined() (*participant.Client, string, string, error) {
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
