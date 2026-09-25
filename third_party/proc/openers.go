package proc

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const DefaultSessionTimeout = 24 * time.Hour

func DefaultEnv() []string {
	return []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
}

func DefaultOpeners(mgr *Manager, timeout time.Duration, env []string) map[string]OpenFunc {
	if timeout <= 0 {
		timeout = DefaultSessionTimeout
	}
	if env == nil {
		env = DefaultEnv()
	}
	return map[string]OpenFunc{
		"shell":   ShellOpener(mgr, timeout, env),
		"command": CommandOpener(mgr, timeout, env),
	}
}

// ShellOpener starts an interactive login shell on a pseudo-terminal.
func ShellOpener(mgr *Manager, timeout time.Duration, env []string) OpenFunc {
	return func(ctx context.Context, spec OpenSpec) (OpenResult, error) {
		if mgr == nil {
			return OpenResult{}, fmt.Errorf("proc manager unavailable")
		}
		binary, args := DefaultShellCommand()
		options := ProcOptions{Binary: binary, Args: args, Env: env}
		info, err := mgr.Start(ctx, Spec{
			Name:    spec.Name,
			Kind:    "shell",
			Command: options.label(),
			Timeout: timeout,
		}, TTY(options))
		if err != nil {
			return OpenResult{}, err
		}
		return OpenResult{Info: info}, nil
	}
}

// CommandOpener runs one command line on a pseudo-terminal, preserving the
// terminal control bytes so a remote client can render it faithfully.
func CommandOpener(mgr *Manager, timeout time.Duration, env []string) OpenFunc {
	return func(ctx context.Context, spec OpenSpec) (OpenResult, error) {
		if mgr == nil {
			return OpenResult{}, fmt.Errorf("proc manager unavailable")
		}
		line := strings.TrimSpace(spec.Command)
		if line == "" {
			return OpenResult{}, fmt.Errorf("command required")
		}
		options := ProcOptions{Line: line, Env: env}
		info, err := mgr.Start(ctx, Spec{
			Name:    spec.Name,
			Kind:    "command",
			Command: line,
			Timeout: timeout,
		}, TTY(options))
		if err != nil {
			return OpenResult{}, err
		}
		return OpenResult{Info: info}, nil
	}
}

// ServiceOpener starts a long-running service on plain pipes. A service is not
// a terminal: its stdout may be a protocol, it is never resized and it is never
// attached to. When probe is non-nil the unit stays in StateStarting until the
// probe it returns reports the service usable.
//
// The opener passes the caller's environment verbatim, because a service is
// usually started with a deliberately narrow environment rather than an
// inherited one.
func ServiceOpener(mgr *Manager, timeout time.Duration, env []string, probe func(OpenSpec) func(context.Context) error) OpenFunc {
	return func(ctx context.Context, spec OpenSpec) (OpenResult, error) {
		if mgr == nil {
			return OpenResult{}, fmt.Errorf("proc manager unavailable")
		}
		if strings.TrimSpace(spec.Command) == "" {
			return OpenResult{}, fmt.Errorf("service command required")
		}
		options := ProcOptions{
			Binary:  spec.Command,
			Args:    append([]string(nil), spec.Args...),
			Env:     env,
			EnvMode: EnvReplace,
		}
		if probe != nil {
			options.Ready = probe(spec)
		}
		info, err := mgr.Start(ctx, Spec{
			Name:    spec.Name,
			Kind:    "service",
			Command: options.label(),
			Timeout: timeout,
		}, Pipe(options))
		if err != nil {
			return OpenResult{}, err
		}
		return OpenResult{Info: info}, nil
	}
}
