//go:build !unix && !windows

package proc

import "os/exec"

func setProcessGroup(*exec.Cmd) {}

func signalProcessGroup(int, Signal) error { return ErrUnsupported }

func exitSignal(*exec.ExitError) string { return "" }
