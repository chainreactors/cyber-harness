//go:build !windows

package harness_test

import (
	"os"
	"os/exec"
)

func configureProcess(*exec.Cmd) {}

func interruptProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer process.Release()
	return process.Signal(os.Interrupt)
}
