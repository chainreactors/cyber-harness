//go:build !windows

package terminal

import "github.com/chainreactors/utils/proc"

func commandShell(string) (string, []string, bool) { return "", nil, true }

func shellProcess(o proc.ProcOptions) proc.Attachment { return proc.TTY(o) }

func posixShellAvailable() bool { return true }

func pipeShell(script string) (string, []string) { return "sh", []string{"-c", script} }

func prepareShellProcessEnv(env []string) []string { return env }

func writeShellCommandBashEnv(string, []string) error { return nil }

func shellBashEnv(string) string { return "" }
