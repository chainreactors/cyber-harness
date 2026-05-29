package tmux

import (
	"os/exec"
	"runtime"
	"sync"
)

var (
	resolvedShell     string
	resolvedShellOnce sync.Once
)

func findShell() string {
	for _, candidate := range []string{
		"/bin/bash", "/usr/bin/bash",
		"/bin/sh", "/usr/bin/sh",
		"bash", "sh",
	} {
		if p, err := exec.LookPath(candidate); err == nil {
			return p
		}
	}
	return "/bin/sh"
}

func shell() string {
	resolvedShellOnce.Do(func() {
		resolvedShell = findShell()
	})
	return resolvedShell
}

// ShellCommand returns an exec.Cmd that runs cmdLine in a shell.
func ShellCommand(cmdLine string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", cmdLine)
	}
	return exec.Command(shell(), "-c", cmdLine)
}
