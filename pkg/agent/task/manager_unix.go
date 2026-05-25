//go:build unix

package task

import (
	"os/exec"
	"syscall"
)

// configureTaskProcessGroup makes the child the leader of a new process
// group so signalProcessGroup can cascade kills to descendants.
func configureTaskProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalProcessGroup delivers SIGTERM (or SIGKILL if hard==true) to the
// process group whose leader has pid. Returns whatever syscall.Kill returns.
func signalProcessGroup(pid int, hard bool) error {
	sig := syscall.SIGTERM
	if hard {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(-pid, sig)
}
