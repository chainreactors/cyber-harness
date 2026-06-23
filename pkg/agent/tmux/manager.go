// Package tmux provides a thin wrapper around the shared pty.Manager from
// github.com/chainreactors/utils/pty. It adds aiscan-specific command routing
// (Command interface, RunCommand, SetCommands, SetWorkDir) and re-exports all
// base types as aliases for backward compatibility.
package tmux

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/utils/pty"
)

// ---------------------------------------------------------------------------
// Type aliases — keep all existing callers compiling without changes.
// ---------------------------------------------------------------------------

type State = pty.State

const (
	StateRunning   = pty.StateRunning
	StateCompleted = pty.StateCompleted
	StateKilled    = pty.StateKilled
	StateFailed    = pty.StateFailed
)

type Info = pty.Info

type EventAction = pty.EventAction

const (
	EventSessionCreated = pty.EventSessionCreated
	EventSessionUpdated = pty.EventSessionUpdated
	EventSessionOutput  = pty.EventSessionOutput
	EventSessionClosed  = pty.EventSessionClosed
)

type Event = pty.Event

type OutputBuffer = pty.OutputBuffer

const (
	DefaultTimeout  = pty.DefaultTimeout
	DefaultBufferCap = pty.DefaultBufferCap
)

// Re-export buffer constructors.
var (
	NewOutputBuffer         = pty.NewOutputBuffer
	NewOutputBufferWithFile = pty.NewOutputBufferWithFile
)

// Re-export shell helpers.
var (
	ShellCommand        = pty.ShellCommand
	DefaultShellCommand = pty.DefaultShellCommand
)

// Re-export formatting.
var FormatCompletion = pty.FormatCompletion

// ---------------------------------------------------------------------------
// Command — aiscan-specific in-process command interface
// ---------------------------------------------------------------------------

// Command is the minimal interface for an in-process command that can be
// executed inside a goroutine-based session. The command package's Command
// interface (which adds Usage()) satisfies this via Go structural subtyping.
type Command interface {
	Name() string
	Execute(ctx context.Context, args []string) error
}

// ---------------------------------------------------------------------------
// RunOpts — extended with WorkDir (not in base pty.RunOpts)
// ---------------------------------------------------------------------------

// RunOpts controls how RunCommand creates a session.
type RunOpts struct {
	Name    string
	Timeout time.Duration
	WorkDir string
	Env     []string
	Ctx     context.Context
}

// ---------------------------------------------------------------------------
// Manager — embeds pty.Manager, adds command routing + event bus
// ---------------------------------------------------------------------------

// Manager wraps pty.Manager and adds aiscan-specific command routing.
type Manager struct {
	*pty.Manager

	events     *eventbus.Bus[Event]
	commands   func(name string) (Command, bool)
	workDir    string
	beforeExec func(w io.Writer)
	afterExec  func()
}

// NewManager creates a Manager backed by a fresh pty.Manager.
func NewManager() *Manager {
	m := &Manager{
		Manager: pty.NewManager(),
		events:  eventbus.New[Event](),
	}
	// Bridge pty.Manager events into the aiscan eventbus.
	m.SetOnEvent(func(ev Event) {
		if m.events != nil {
			m.events.Emit(ev)
		}
	})
	return m
}

// Subscribe registers an event listener and returns an unsubscribe function.
func (m *Manager) Subscribe(fn func(Event)) func() {
	if fn == nil {
		return func() {}
	}
	return m.events.Subscribe(fn)
}

// SetCommands injects the lookup function used by RunCommand to detect
// in-process commands. The function is typically a closure over a
// CommandRegistry in the calling package.
func (m *Manager) SetCommands(fn func(name string) (Command, bool)) {
	m.commands = fn
}

// SetExecHooks sets callbacks invoked before/after each in-process command
// execution. beforeExec receives the session's io.Writer so the caller can
// redirect a global output sink; afterExec resets it.
func (m *Manager) SetExecHooks(before func(w io.Writer), after func()) {
	m.beforeExec = before
	m.afterExec = after
}

// SetWorkDir sets the default working directory for shell sessions created
// by RunCommand.
func (m *Manager) SetWorkDir(dir string) {
	m.workDir = dir
}


// RunCommand creates a session for the given command line. If the first
// token matches a registered in-process Command, the command runs in a
// goroutine-based session (CreateFunc). Otherwise it runs as a shell
// command in a PTY session (Create).
//
// Pipe support: "pseudo-cmd args | shell-pipeline" is supported. The
// pseudo-command runs in-process with its output captured to a buffer,
// then the buffer is piped as stdin to the shell pipeline via sh -c.
func (m *Manager) RunCommand(cmdLine string, opts RunOpts) (Info, error) {
	cmdLine = stripCommentsAndBlanks(cmdLine)
	if strings.TrimSpace(cmdLine) == "" {
		return Info{}, errors.New("empty command")
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	workDir := opts.WorkDir
	if workDir == "" {
		workDir = m.workDir
	}

	token := firstCommandToken(cmdLine)

	resolve := m.commands

	if resolve != nil && token != "" {
		if cmd, ok := resolve(token); ok {
			pseudoPart, shellPipeline, hasPipe := splitPipeline(cmdLine)

			tokens, err := SplitCommandLine(pseudoPart)
			if err != nil {
				return Info{}, err
			}
			if len(tokens) > 1 {
				if _, valErr := stripShellSyntax(tokens[1:]); valErr != nil {
					return Info{}, valErr
				}
			}

			name := opts.Name
			if name == "" {
				name = token
			}
			args := tokens[1:]

			if hasPipe && shellPipeline != "" {
				return m.runPipedPseudo(opts.Ctx, cmd, args, shellPipeline, name, timeout, workDir, opts.Env)
			}

			return m.CreateFunc(opts.Ctx, name, timeout, func(ctx context.Context, w io.Writer) error {
				if m.beforeExec != nil {
					m.beforeExec(w)
				}
				if m.afterExec != nil {
					defer m.afterExec()
				}
				return cmd.Execute(ctx, args)
			})
		}
	}

	return m.Create(workDir, cmdLine, opts.Name, timeout, opts.Env, "")
}

// runPipedPseudo runs a pseudo-command in-process, captures its output,
// then pipes it as stdin to a shell pipeline. Everything runs inside a
// single CreateFunc session so the caller sees one session ID.
func (m *Manager) runPipedPseudo(
	ctx context.Context,
	cmd Command, args []string,
	pipeline string,
	name string, timeout time.Duration,
	workDir string, env []string,
) (Info, error) {
	return m.CreateFunc(ctx, name, timeout, func(ctx context.Context, w io.Writer) error {
		// Phase 1: run pseudo-command, capture output to buffer.
		var buf bytes.Buffer
		if m.beforeExec != nil {
			m.beforeExec(&buf)
		}
		execErr := cmd.Execute(ctx, args)
		if m.afterExec != nil {
			m.afterExec()
		}
		if execErr != nil {
			_, _ = w.Write(buf.Bytes())
			return execErr
		}

		// Phase 2: pipe captured output through shell pipeline.
		sh := exec.CommandContext(ctx, "sh", "-c", pipeline)
		sh.Stdin = &buf
		sh.Stdout = w
		sh.Stderr = w
		if workDir != "" {
			sh.Dir = workDir
		}
		if len(env) > 0 {
			sh.Env = append(os.Environ(), env...)
		}
		return sh.Run()
	})
}

func stripCommentsAndBlanks(input string) string {
	lines := strings.Split(input, "\n")
	var kept []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
