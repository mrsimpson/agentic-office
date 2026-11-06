// Command office is the participant client (bb-participant-client): the office CLI and the log
// replicator ("office replicator run", started by "office meeting join"). Later also the MCP
// server and the ears. See client/cli and README.md.
package main

import (
	"os"

	"github.com/mrsimpson/agentic-office/client/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr)) }
