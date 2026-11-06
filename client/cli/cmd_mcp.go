package cli

import (
	"flag"
	"io"

	"github.com/mrsimpson/agentic-office/client/mcp"
)

// cmdMCP runs the MCP server (bb-mcp) on stdin and stdout. An MCP client starts it. The log
// replicators of the sessions it joins run in this process and stop when it ends.
func cmdMCP(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		s := &mcp.Server{Config: e.cfg, Session: e.session, Version: Version, Stderr: e.stderr}
		return s.Run(e.ctx, io.NopCloser(e.stdin), nopWriteCloser{e.stdout})
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
