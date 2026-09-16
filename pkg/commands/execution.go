package commands

import (
	"context"
	"io"
	"sync"

	"github.com/chainreactors/cyber/agent/tmux"
	"github.com/chainreactors/utils/pty"
)

// Execution contains one invocation's arguments, streams and command details.
// When backed by a terminal, ID addresses the manager's session. In-process
// invocations may instead carry a call ID and have no terminal session.
// Process state belongs to the manager and is read through Session.
type Execution struct {
	ID      string
	Command string
	Args    []string
	Dir     string
	Env     []string

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	Details any

	manager *tmux.Manager
	mu      sync.RWMutex
	// idReady closes after the execution receives either its manager-assigned
	// session ID or its in-process call ID. A built-in command may start before
	// CreateFunc returns, so correlation waits on this boundary.
	idReady chan struct{}

	cancelProcess context.CancelCauseFunc
	detachParent  func() bool
	stopCancel    func() bool
	releaseEgress func()
	processDone   chan struct{}
	processOnce   sync.Once
}

// BindProcessControl connects the local operation cancellation scope to the
// actual managed session. These handles stay process-local and never enter tool
// arguments or the AOP protocol.
func (e *Execution) BindProcessControl(ctx context.Context, cancel context.CancelCauseFunc, detachParent func() bool, releaseEgress func()) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.cancelProcess = cancel
	e.detachParent = detachParent
	e.stopCancel = context.AfterFunc(ctx, func() { _ = e.Kill() })
	e.releaseEgress = releaseEgress
	e.processDone = make(chan struct{})
	e.mu.Unlock()
}

// DetachParent transfers a still-running execution to the process manager.
// It is used only after the caller explicitly chooses background execution.
func (e *Execution) DetachParent() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	stop := e.detachParent
	e.detachParent = nil
	e.mu.Unlock()
	return stop == nil || stop()
}

// FinishProcess releases the process control handles bound earlier and wakes
// WaitProcessCompletion with the final cause.
func (e *Execution) FinishProcess(cause error) {
	if e == nil {
		return
	}
	e.mu.Lock()
	stopCancel := e.stopCancel
	stopParent := e.detachParent
	cancel := e.cancelProcess
	releaseEgress := e.releaseEgress
	e.stopCancel = nil
	e.detachParent = nil
	e.cancelProcess = nil
	e.releaseEgress = nil
	e.mu.Unlock()
	if stopCancel != nil {
		stopCancel()
	}
	if stopParent != nil {
		stopParent()
	}
	if cancel != nil {
		cancel(cause)
	}
	if releaseEgress != nil {
		releaseEgress()
	}
	e.processOnce.Do(func() {
		e.mu.RLock()
		done := e.processDone
		e.mu.RUnlock()
		if done != nil {
			close(done)
		}
	})
}

// WaitProcessCompletion waits for the real process boundary, including its
// completion hooks and call-scoped egress drain. A foreground caller uses this
// after the native session exits; explicitly backgrounded callers do not.
func (e *Execution) WaitProcessCompletion(ctx context.Context) error {
	if e == nil {
		return nil
	}
	e.mu.RLock()
	done := e.processDone
	e.mu.RUnlock()
	if done == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// NewExecution builds the invocation record handed to Command.Run.
func NewExecution(manager *tmux.Manager, command string, args []string, dir string, env []string) *Execution {
	return &Execution{
		Command: command,
		Args:    append([]string(nil), args...),
		Dir:     dir,
		Env:     append([]string(nil), env...),
		Stdin:   nil,
		Stdout:  io.Discard,
		Stderr:  io.Discard,
		manager: manager,
		idReady: make(chan struct{}),
	}
}

// BindID publishes the manager session ID or the in-process call ID and
// releases any waiter blocked on ID readiness. The first binding wins.
func (e *Execution) BindID(id string) {
	e.mu.Lock()
	if e.ID != "" {
		e.mu.Unlock()
		return
	}
	e.ID = id
	ready := e.idReady
	e.idReady = nil
	e.mu.Unlock()
	if ready != nil {
		close(ready)
	}
}

func (e *Execution) waitID(ctx context.Context) (string, error) {
	if e == nil {
		return "", nil
	}
	e.mu.RLock()
	id, ready := e.ID, e.idReady
	e.mu.RUnlock()
	if id != "" || ready == nil {
		return id, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ready:
		e.mu.RLock()
		defer e.mu.RUnlock()
		return e.ID, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// SetIO attaches the invocation's streams once the terminal decides how to
// present them.
func (e *Execution) SetIO(stdin io.Reader, stdout, stderr io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Stdin = stdin
	e.Stdout = stdout
	e.Stderr = stderr
}

// SetDetails attaches the terminal-owned command detail payload.
func (e *Execution) SetDetails(details any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Details = details
}

// Session returns the manager's current native snapshot. false means there is
// no retained terminal session; a call ID alone does not imply a process.
func (e *Execution) Session() (pty.Info, bool) {
	if e == nil {
		return pty.Info{}, false
	}
	e.mu.RLock()
	id := e.ID
	e.mu.RUnlock()
	if id == "" || e.manager == nil {
		return pty.Info{}, false
	}
	return e.manager.Get(id)
}

// Wait waits for the PTY session. Canceling the wait also kills the session,
// matching the previous foreground Bash execution behavior.
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
		return nil
	case <-ctx.Done():
		_ = e.manager.Kill(id)
		<-done
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
