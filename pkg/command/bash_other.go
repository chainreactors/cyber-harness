//go:build !unix

package command

import (
	"fmt"
	"os/exec"
)

func configureBackgroundCommand(_ *exec.Cmd) {}

func backgroundStatusCommand(pid int) string {
	return fmt.Sprintf("check process %d", pid)
}

func backgroundStopCommand(pid int) string {
	return fmt.Sprintf("stop process %d", pid)
}

func terminateBackgroundProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func killBackgroundProcess(cmd *exec.Cmd) error {
	return terminateBackgroundProcess(cmd)
}
