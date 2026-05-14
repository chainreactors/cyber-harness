//go:build unix

package command

import (
	"fmt"
	"os/exec"
	"syscall"
)

func configureBackgroundCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func backgroundStatusCommand(pid int) string {
	return fmt.Sprintf("kill -0 -- -%d", pid)
}

func backgroundStopCommand(pid int) string {
	return fmt.Sprintf("kill -- -%d", pid)
}

func terminateBackgroundProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

func killBackgroundProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
