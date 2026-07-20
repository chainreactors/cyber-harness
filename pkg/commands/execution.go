package commands

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/tmux"
)

// Execution is one shell or built-in command invocation. Its ID is always the
// ID of the underlying PTY session, so existing tmux attach/read/write/kill
// operations continue to address the same runtime object.
type Execution struct {
	ID      string
	Command string
	Args    []string
	Dir     string
	Env     []string

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	State     tmux.State
	ExitCode  int
	StartedAt time.Time
	EndedAt   time.Time
	KillCause string
	Details   any

	manager *tmux.Manager
	mu      sync.RWMutex
}

func newExecution(manager *tmux.Manager, command string, args []string, dir string, env []string) *Execution {
	return &Execution{
		Command: command,
		Args:    append([]string(nil), args...),
		Dir:     dir,
		Env:     append([]string(nil), env...),
		Stdin:   nil,
		Stdout:  io.Discard,
		Stderr:  io.Discard,
		State:   tmux.StateRunning,
		manager: manager,
	}
}

func (e *Execution) bind(info tmux.Info) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ID = info.ID
	e.applyInfoLocked(info)
}

func (e *Execution) setIO(stdin io.Reader, stdout, stderr io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Stdin = stdin
	e.Stdout = stdout
	e.Stderr = stderr
}

func (e *Execution) setDetails(details any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Details = details
}

func (e *Execution) applyInfoLocked(info tmux.Info) {
	e.State = info.State
	e.ExitCode = info.ExitCode
	e.StartedAt = info.StartedAt
	e.EndedAt = info.EndedAt
	e.KillCause = info.KillCause
}

func (e *Execution) refresh() {
	e.mu.RLock()
	id := e.ID
	e.mu.RUnlock()
	if id == "" || e.manager == nil {
		return
	}
	if info, ok := e.manager.Get(id); ok {
		e.mu.Lock()
		e.applyInfoLocked(info)
		e.mu.Unlock()
	}
}

// Wait waits for the PTY session. Cancelling the wait also kills the session,
// matching the previous foreground Bash execution behaviour.
func (e *Execution) Wait(ctx context.Context) error {
	e.mu.RLock()
	id := e.ID
	e.mu.RUnlock()
	if id == "" || e.manager == nil {
		return nil
	}
	done := e.manager.Done(id)
	select {
	case <-done:
		e.refresh()
		return nil
	case <-ctx.Done():
		_ = e.manager.Kill(id)
		<-done
		e.refresh()
		return ctx.Err()
	}
}

func (e *Execution) Kill() error {
	e.mu.RLock()
	id := e.ID
	e.mu.RUnlock()
	if id == "" || e.manager == nil {
		return nil
	}
	return e.manager.Kill(id)
}

func (e *Execution) Duration() time.Duration {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.StartedAt.IsZero() {
		return 0
	}
	if !e.EndedAt.IsZero() {
		return e.EndedAt.Sub(e.StartedAt)
	}
	return time.Since(e.StartedAt)
}
