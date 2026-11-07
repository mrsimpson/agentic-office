package archive

import (
	"os/exec"
	"syscall"
)

// dieWithHub makes git stop when the hub dies, so no git process writes the repository after
// the hub is gone.
func dieWithHub(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
