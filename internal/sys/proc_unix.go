//go:build !windows

package sys

import (
	"os/exec"
	"syscall"
)

func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killTree(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
