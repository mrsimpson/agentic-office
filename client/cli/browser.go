package cli

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"

	"golang.org/x/term"
)

// interactive reports whether a person is at the terminal: stderr, where join prints the stage
// link, is a terminal. Agents, scripts and tests have none, so they never open a browser.
func (e *env) interactive() bool {
	f, ok := e.stderr.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// openBrowser opens a web address in the default browser of the desktop. It starts the opener
// and does not wait for it. Only http and https addresses are opened.
func openBrowser(address string) error {
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("not a web address: %q", address)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", address)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	default:
		cmd = exec.Command("xdg-open", address)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
