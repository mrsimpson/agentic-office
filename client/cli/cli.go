package cli

import (
	"fmt"
	"io"
)

// Version is set at build time with -ldflags "-X .../client/cli.Version=...".
var Version = "dev"

const usage = `usage: office <command>

Commands:
  version   print the version
  help      print this help

More commands follow with the next work packages.
`

// Run runs the CLI and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, Version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "office: unknown command %q\n\n%s", args[0], usage)
	return 2
}
