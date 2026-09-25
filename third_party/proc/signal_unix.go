//go:build unix

package proc

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so that a stop
// addresses the whole tree rather than just the leader.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalProcessGroup delivers one rung of the ladder to the process group whose
// leader has pid.
func signalProcessGroup(pid int, sig Signal) error {
	if pid <= 0 {
		return nil
	}
	var delivered syscall.Signal
	switch sig {
	case SigInterrupt:
		delivered = syscall.SIGINT
	case SigTerminate:
		delivered = syscall.SIGTERM
	default:
		delivered = syscall.SIGKILL
	}
	return syscall.Kill(-pid, delivered)
}

func exitSignal(err *exec.ExitError) string {
	status, ok := err.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return ""
	}
	return status.Signal().String()
}
