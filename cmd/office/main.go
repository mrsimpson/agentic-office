// Command office is the participant client (bb-participant-client): the office CLI, the log
// replicator ("office replicator run", started by "office meeting join") and the ears
// ("office meeting join --mic" or "--wav"). Later also the MCP server. See client/cli and
// README.md.
//
// The ears link whisper.cpp through cgo; build with "make build" (see README.md).
package main

import (
	"context"
	"os"

	"github.com/mrsimpson/agentic-office/client/cli"
	"github.com/mrsimpson/agentic-office/client/ears"
	"github.com/mrsimpson/agentic-office/client/participant"
)

func main() {
	cli.CheckEars = func(o *cli.EarsOptions) error {
		cfg := earsConfig(*o)
		if err := cfg.Prepare(); err != nil {
			return err
		}
		return nil
	}
	cli.StartEars = func(ctx context.Context, c *participant.Client, sid string, o cli.EarsOptions,
		status func(cmdID, text string, s participant.Status), paused func() bool) error {
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
