//go:build windows

package sys

import (
	"os/exec"
	"strconv"
)

func Detach(cmd *exec.Cmd) {}

func killTree(pid int) error {
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
