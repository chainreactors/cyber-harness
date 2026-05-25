//go:build !unix

package task

import "os/exec"

func configureTaskProcessGroup(_ *exec.Cmd) {}

func signalProcessGroup(_ int, _ bool) error {
	return nil
}
