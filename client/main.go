// Command office is the participant client (bb-participant-client): the office CLI, the log
// replicator ("office replicator run", started by "office meeting join"), the ears
// ("office meeting join --mic" or "--wav"), the MCP server ("office mcp", client/mcp) and the
// agent runner ("office agent run", client/agentrunner). See
// client/cli and README.md.
//
// The ears link whisper.cpp through cgo; build with "make build" (see README.md).
package main

import (
	"context"
	"io"
	"os"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	"github.com/mrsimpson/agentic-office/client/replicator"

	"github.com/mrsimpson/agentic-office/client/agentrunner"
	"github.com/mrsimpson/agentic-office/client/cli"
	"github.com/mrsimpson/agentic-office/client/core"
	"github.com/mrsimpson/agentic-office/client/ears"
	"github.com/mrsimpson/agentic-office/client/mcp"
)

func main() {
	cli.Dial = core.Dial
	cli.NewReplicator = replicator.New
	cli.RunMCP = func(ctx context.Context, o cli.MCPOptions) error {
		s := &mcp.Server{Config: o.Config, Session: o.Session, Version: o.Version, Stderr: o.Stderr,
			Dial: core.Dial, NewReplicator: replicator.New}
		return s.Run(ctx, io.NopCloser(o.Stdin), nopWriteCloser{o.Stdout})
	}
	cli.RunAgent = func(ctx context.Context, o cli.AgentOptions) error {
		return agentrunner.Run(ctx, agentrunner.Options{Config: o.Config, Session: o.Session, Role: o.Role,
			Harness: o.Harness, Allow: o.Allow, Cwd: o.Cwd, Office: o.Office, Version: o.Version, Stderr: o.Stderr,
			Dial: core.Dial, NewReplicator: replicator.New})
	}
	cli.CheckEars = func(ctx context.Context, o *cli.EarsOptions, progress func(string)) error {
		cfg := earsConfig(*o)
		return cfg.Prepare(ctx, progress)
	}
	cli.DownloadModels = func(ctx context.Context, o cli.ModelOptions, progress func(string)) error {
		return ears.DownloadModels(ctx, o.Name, o.Language, o.Fast, progress)
	}
	cli.ListModels = func(o cli.ModelOptions) (cli.ModelList, error) {
		ms, c, err := ears.Catalogue(o.Name, o.Language, o.Fast)
		if err != nil {
			return cli.ModelList{}, err
		}
		l := cli.ModelList{Selected: c.Name, Language: c.Language, Note: c.Note}
		if l.Selected == "" {
			l.Selected = c.Path
		}
		for _, m := range ms {
			l.Models = append(l.Models, cli.ModelEntry(m))
		}
		return l, nil
	}
	cli.StartEars = func(ctx context.Context, c coreapi.Client, sid string, o cli.EarsOptions,
		status func(cmdID, text string, s coreapi.Status), paused func() bool) error {
		e := &ears.Ears{Config: earsConfig(o), Client: c, SessionID: sid, Status: status, Paused: paused, Names: o.Names}
		if o.Report != nil {
			e.Report = func(r ears.Report) { o.Report(r.String(), r) }
		}
		return e.Run(ctx)
	}
	cli.ExtractVocabulary = func(ctx context.Context, o cli.VocabularyOptions, progress func(string)) (cli.VocabularyResult, error) {
		x, err := ears.ExtractVocabulary(ctx, ears.ExtractOptions{Dir: o.Dir, Max: o.Max, NoModel: o.NoModel}, progress)
		if err != nil {
			return cli.VocabularyResult{}, err
		}
		if err := ears.WriteVocabulary(o.Out, o.Dir, x, o.Force); err != nil {
			return cli.VocabularyResult{}, err
		}
		r := cli.VocabularyResult{File: o.Out, Documents: x.Documents, Model: x.Model, Terms: []cli.VocabularyTerm{}}
		for _, t := range x.Terms {
			r.Terms = append(r.Terms, cli.VocabularyTerm(t))
		}
		return r, nil
	}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}

func earsConfig(o cli.EarsOptions) ears.Config {
	return ears.Config{Mic: o.Mic, WAV: o.WAV, Model: o.Model, Fast: o.Fast, VADModel: o.VADModel, Language: o.Language,
		Threads: o.Threads, EndSilence: o.EndSilence, TurnSilence: o.TurnSilence, Vocabulary: o.Vocabulary, Context: o.Context}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
