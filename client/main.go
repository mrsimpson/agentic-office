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

	"github.com/mrsimpson/agentic-office/client/agentrunner"
	"github.com/mrsimpson/agentic-office/client/cli"
	"github.com/mrsimpson/agentic-office/client/core"
	"github.com/mrsimpson/agentic-office/client/ears"
	"github.com/mrsimpson/agentic-office/client/mcp"
)

func main() {
	cli.RunMCP = func(ctx context.Context, o cli.MCPOptions) error {
		s := &mcp.Server{Config: o.Config, Session: o.Session, Version: o.Version, Stderr: o.Stderr}
		return s.Run(ctx, io.NopCloser(o.Stdin), nopWriteCloser{o.Stdout})
	}
	cli.RunAgent = func(ctx context.Context, o cli.AgentOptions) error {
		return agentrunner.Run(ctx, agentrunner.Options{Config: o.Config, Session: o.Session, Role: o.Role,
			Harness: o.Harness, Allow: o.Allow, Cwd: o.Cwd, Office: o.Office, Version: o.Version, Stderr: o.Stderr,
			TopicTask: o.TopicTask, Decision: agentrunner.DecisionModel{Command: o.DecideCommand, URL: o.DecideURL,
				Model: o.DecideModel, APIKey: o.DecideKey, Timeout: o.DecideTimeout}})
	}
	cli.CheckEars = func(ctx context.Context, o *cli.EarsOptions, progress func(string)) error {
		cfg := earsConfig(*o)
		return cfg.Prepare(ctx, progress)
	}
	cli.DownloadModels = ears.DownloadModels
	cli.StartEars = func(ctx context.Context, c *core.Client, sid string, o cli.EarsOptions,
		status func(cmdID, text string, s core.Status), paused func() bool) error {
		e := &ears.Ears{Config: earsConfig(o), Client: c, SessionID: sid, Status: status, Paused: paused}
		if o.Report != nil {
			e.Report = func(r ears.Report) { o.Report(r.String(), r) }
		}
		return e.Run(ctx)
	}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}

func earsConfig(o cli.EarsOptions) ears.Config {
	return ears.Config{Mic: o.Mic, WAV: o.WAV, Model: o.Model, VADModel: o.VADModel, Language: o.Language,
		Threads: o.Threads, EndSilence: o.EndSilence}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
