//go:build unix

package task

import "syscall"

// signalProcessGroup delivers SIGTERM (or SIGKILL if hard==true) to the
// process group whose leader has pid. Returns whatever syscall.Kill returns.
func signalProcessGroup(pid int, hard bool) error {
	sig := syscall.SIGTERM
	if hard {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(-pid, sig)
}
