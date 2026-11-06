// Command office is the participant client (bb-participant-client).
package main

import (
	"os"

	"github.com/mrsimpson/agentic-office/client/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr)) }
