package cli

import (
	"context"
	"errors"
	"flag"
	"io"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

// MCPOptions are what the MCP server (bb-mcp) needs from the CLI.
type MCPOptions struct {
	Config  coreapi.Config
	Session string // default session (--session, OFFICE_SESSION)
	Version string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// RunMCP is the hook for the MCP server (client/mcp). The wiring of the participant client
// (client/) sets it, so that the CLI does not depend on the MCP server.
var RunMCP func(ctx context.Context, opts MCPOptions) error

// cmdMCP runs the MCP server on stdin and stdout. An MCP client starts it. The log replicators of
// the sessions it joins run in this process and stop when it ends.
func cmdMCP(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if RunMCP == nil {
			return errors.New("this office binary has no MCP server")
		}
		return RunMCP(e.ctx, MCPOptions{Config: e.cfg, Session: e.session, Version: Version,
			Stdin: e.stdin, Stdout: e.stdout, Stderr: e.stderr})
	}
}
