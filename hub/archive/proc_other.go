//go:build !linux

package archive

import "os/exec"

func dieWithHub(*exec.Cmd) {}
