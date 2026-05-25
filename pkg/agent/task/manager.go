// Package task provides a background-task manager for long-running shell
// commands and in-process closures launched by the agent. Output is buffered
// in memory (no disk I/O). An optional TaskObserver receives structured events
// (start, output, completion) modeled after the scan pipeline's Observe pattern.
package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type State string

const (
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateKilled    State = "killed"
	StateFailed    State = "failed"
)

const (
	DefaultTimeout = 30 * time.Minute
	killGrace      = 5 * time.Second
	shutdownGrace  = 2 * time.Second
)

type EventKind string

const (
	EventStart      EventKind = "start"
	EventOutput     EventKind = "output"
	EventCompletion EventKind = "completion"
)

type TaskEvent struct {
	Kind      EventKind
	TaskID    string
	TaskInfo  Info
	Output    []byte // EventOutput only
	Killed    bool
	KillCause string
}

type TaskObserver func(TaskEvent)

type Info struct {
	ID         string    `json:"id"`
	Name       string    `json:"name,omitempty"`
	Command    string    `json:"command"`
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at,omitempty"`
	ExitCode   int       `json:"exit_code"`
	State      State     `json:"state"`
	Filename   string    `json:"filename,omitempty"`
}

type SpawnOption struct {
	Filename string // optional: tee stdout to this file; empty = memory only
}

type task struct {
	Info
	cmd       *exec.Cmd
	output    *OutputBuffer
	done      chan struct{}
	cancel    context.CancelFunc
	killCause string
}

type InProcessFn func(ctx context.Context, out io.Writer) error

type ProducerRegistrar func(name string) func()

type Manager struct {
	mu               sync.Mutex
	tasks            map[string]*task
	observe          TaskObserver
	registerProducer ProducerRegistrar
	bufCap           int
}

func NewManager() *Manager {
	return &Manager{tasks: make(map[string]*task)}
}

func (m *Manager) SetObserver(fn TaskObserver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observe = fn
}

func (m *Manager) SetProducerRegistrar(fn ProducerRegistrar) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registerProducer = fn
}

func (m *Manager) newProducer(id string) func() {
	m.mu.Lock()
	reg := m.registerProducer
	m.mu.Unlock()
	if reg == nil {
		return nil
	}
	return reg("task:" + id)
}

func (m *Manager) bufferCap() int {
	if m.bufCap > 0 {
		return m.bufCap
	}
	return DefaultBufferCap
}

func (m *Manager) newBuffer(outputFile string) (*OutputBuffer, error) {
	if outputFile != "" {
		return NewOutputBufferWithFile(m.bufferCap(), outputFile)
	}
	return NewOutputBuffer(m.bufferCap()), nil
}

func (m *Manager) emit(ev TaskEvent) {
	m.mu.Lock()
	obs := m.observe
	m.mu.Unlock()
	if obs == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "task observer panic: %v\n", r)
		}
	}()
	obs(ev)
}

func (m *Manager) Adopt(cmd *exec.Cmd, output *OutputBuffer, name string, timeout time.Duration) Info {
	id, _ := genID()
	if name == "" {
		name = "adopted"
	}
	info := Info{
		ID:        id,
		Name:      name,
		Command:   strings.Join(cmd.Args, " "),
		PID:       cmd.Process.Pid,
		StartedAt: time.Now(),
		State:     StateRunning,
	}
	t := &task{Info: info, cmd: cmd, output: output, done: make(chan struct{})}
	output.onWrite = func(p []byte) {
		m.emit(TaskEvent{Kind: EventOutput, TaskID: id, TaskInfo: t.Info, Output: p})
	}

	m.mu.Lock()
	m.tasks[id] = t
	m.mu.Unlock()

	m.emit(TaskEvent{Kind: EventStart, TaskID: id, TaskInfo: info})
	go m.supervise(t, timeout)
	return info
}

func (m *Manager) Spawn(workDir, cmdLine, name string, timeout time.Duration, opts ...SpawnOption) (Info, error) {
	if strings.TrimSpace(cmdLine) == "" {
		return Info{}, errors.New("empty command")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if name == "" {
		name = labelFromCommand(cmdLine)
	}
	var opt SpawnOption
	if len(opts) > 0 {
		opt = opts[0]
	}

	id, err := genID()
	if err != nil {
		return Info{}, fmt.Errorf("generate id: %w", err)
	}

	buf, err := m.newBuffer(opt.Filename)
	if err != nil {
		return Info{}, err
	}

	c := ShellCommand(cmdLine)
	c.Dir = workDir
	c.Stdin = nil
	c.Stdout = buf
	c.Stderr = buf
	configureTaskProcessGroup(c)

	if err := c.Start(); err != nil {
		return Info{}, fmt.Errorf("start: %w", err)
	}

	info := Info{
		ID:         id,
		Name:       name,
		Command:    cmdLine,
		PID:        c.Process.Pid,
		StartedAt:  time.Now(),
		State:      StateRunning,
		Filename: opt.Filename,
	}
	t := &task{Info: info, cmd: c, output: buf, done: make(chan struct{})}

	buf.onWrite = func(p []byte) {
		m.emit(TaskEvent{Kind: EventOutput, TaskID: id, TaskInfo: t.Info, Output: p})
	}

	m.mu.Lock()
	m.tasks[id] = t
	m.mu.Unlock()

	m.emit(TaskEvent{Kind: EventStart, TaskID: id, TaskInfo: info})
	go m.supervise(t, timeout)
	return info, nil
}

func (m *Manager) SpawnInProcess(label, cmdDisplay string, timeout time.Duration, fn InProcessFn, opts ...SpawnOption) (Info, error) {
	if fn == nil {
		return Info{}, errors.New("nil function")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	name := label
	if name == "" {
		name = labelFromCommand(cmdDisplay)
	}
	var opt SpawnOption
	if len(opts) > 0 {
		opt = opts[0]
	}

	id, err := genID()
	if err != nil {
		return Info{}, fmt.Errorf("generate id: %w", err)
	}

	buf, err := m.newBuffer(opt.Filename)
	if err != nil {
		return Info{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())

	info := Info{
		ID:         id,
		Name:       name,
		Command:    cmdDisplay,
		StartedAt:  time.Now(),
		State:      StateRunning,
		Filename: opt.Filename,
	}
	t := &task{Info: info, output: buf, done: make(chan struct{}), cancel: cancel}

	buf.onWrite = func(p []byte) {
		m.emit(TaskEvent{Kind: EventOutput, TaskID: id, TaskInfo: t.Info, Output: p})
	}

	m.mu.Lock()
	m.tasks[id] = t
	m.mu.Unlock()

	m.emit(TaskEvent{Kind: EventStart, TaskID: id, TaskInfo: info})
	go m.superviseInProcess(t, fn, ctx, timeout)
	return info, nil
}

func (m *Manager) superviseInProcess(t *task, fn InProcessFn, ctx context.Context, timeout time.Duration) {
	done := m.newProducer(t.ID)
	defer func() {
		if done != nil {
			done()
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	runErr := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				runErr <- fmt.Errorf("panic in background task: %v", r)
			}
		}()
		runErr <- fn(ctx, t.output)
	}()

	var (
		fnErr     error
		killed    bool
		killCause string
	)
	select {
	case err := <-runErr:
		fnErr = err
	case <-timer.C:
		killed, killCause = true, fmt.Sprintf("timeout after %s", timeout)
		m.markKillCause(t, killCause)
		t.cancel()
		fnErr = <-runErr
	}

	recordedKillCause := m.killCause(t)
	if recordedKillCause != "" {
		killed = true
		killCause = recordedKillCause
	}

	state, exitCode := StateCompleted, 0
	if fnErr != nil {
		if killed {
			state, exitCode = StateKilled, -1
		} else {
			state, exitCode = StateFailed, 1
			t.output.AppendError(fnErr.Error())
		}
	} else if killed {
		state = StateKilled
	}

	m.mu.Lock()
	t.EndedAt = time.Now()
	t.ExitCode = exitCode
	t.State = state
	infoCopy := t.Info
	m.mu.Unlock()

	t.output.Close()
	close(t.done)
	m.emit(TaskEvent{Kind: EventCompletion, TaskID: t.ID, TaskInfo: infoCopy, Killed: killed, KillCause: killCause})
}

func (m *Manager) supervise(t *task, timeout time.Duration) {
	done := m.newProducer(t.ID)
	defer func() {
		if done != nil {
			done()
		}
	}()

	waitDone := make(chan error, 1)
	go func() { waitDone <- t.cmd.Wait() }()

	var (
		waitErr   error
		killed    bool
		killCause string
	)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-waitDone:
		waitErr = err
	case <-timer.C:
		killed, killCause = true, fmt.Sprintf("timeout after %s", timeout)
		m.markKillCause(t, killCause)
		m.forceKillTask(t)
		waitErr = <-waitDone
	}

	recordedKillCause := m.killCause(t)
	if recordedKillCause != "" {
		killed = true
		killCause = recordedKillCause
	}

	state, exitCode := StateCompleted, 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.As(waitErr, &exitErr):
			exitCode = exitErr.ExitCode()
			if killed {
				state = StateKilled
			} else {
				state = StateFailed
			}
		default:
			exitCode = -1
			state = StateFailed
		}
	} else if killed {
		state = StateKilled
	}

	m.mu.Lock()
	t.EndedAt = time.Now()
	t.ExitCode = exitCode
	t.State = state
	infoCopy := t.Info
	m.mu.Unlock()

	t.output.Close()
	close(t.done)
	m.emit(TaskEvent{Kind: EventCompletion, TaskID: t.ID, TaskInfo: infoCopy, Killed: killed, KillCause: killCause})
}

func (m *Manager) forceKillTask(t *task) {
	if t.cmd == nil {
		if t.cancel != nil {
			t.cancel()
		}
		return
	}
	if t.cmd.Process == nil {
		return
	}
	_ = signalProcessGroup(t.cmd.Process.Pid, false)
	timer := time.NewTimer(killGrace)
	defer timer.Stop()
	select {
	case <-t.done:
		return
	case <-timer.C:
	}
	_ = signalProcessGroup(t.cmd.Process.Pid, true)
}

func (m *Manager) Kill(id string) error {
	m.mu.Lock()
	t, ok := m.tasks[id]
	if ok {
		select {
		case <-t.done:
		default:
			if t.killCause == "" {
				t.killCause = "killed by user"
			}
		}
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("no such task: %s", id)
	}
	select {
	case <-t.done:
		return nil
	default:
	}
	go m.forceKillTask(t)
	return nil
}

func (m *Manager) killAll() {
	m.mu.Lock()
	running := make([]*task, 0, len(m.tasks))
	for _, t := range m.tasks {
		select {
		case <-t.done:
		default:
			running = append(running, t)
		}
	}
	m.mu.Unlock()

	for _, t := range running {
		m.markKillCause(t, "shutdown")
		if t.cmd != nil && t.cmd.Process != nil {
			_ = signalProcessGroup(t.cmd.Process.Pid, false)
		}
		if t.cancel != nil {
			t.cancel()
		}
	}
	deadline := time.After(killGrace)
	for _, t := range running {
		select {
		case <-t.done:
		case <-deadline:
			if t.cmd != nil && t.cmd.Process != nil {
				_ = signalProcessGroup(t.cmd.Process.Pid, true)
			}
		}
	}
	finalDeadline := time.After(shutdownGrace)
	for _, t := range running {
		select {
		case <-t.done:
		case <-finalDeadline:
		}
	}
}

func (m *Manager) Shutdown() { m.killAll() }

func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t.Info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

func (m *Manager) RunningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, t := range m.tasks {
		if t.State == StateRunning {
			count++
		}
	}
	return count
}

func (m *Manager) Get(id string) (Info, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return Info{}, false
	}
	return t.Info, true
}

func (m *Manager) Peek(id string, n int) (string, error) {
	m.mu.Lock()
	t, ok := m.tasks[id]
	m.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no such task: %s", id)
	}
	if n <= 0 {
		n = 30
	}
	return t.output.TailLines(n), nil
}

func (m *Manager) PeekSince(id string, offset int64) (string, int64, error) {
	output, newOffset, _, err := m.PeekSinceLimit(id, offset, 0)
	return output, newOffset, err
}

func (m *Manager) PeekSinceLimit(id string, offset int64, maxBytes int64) (string, int64, bool, error) {
	m.mu.Lock()
	t, ok := m.tasks[id]
	m.mu.Unlock()
	if !ok {
		return "", 0, false, fmt.Errorf("no such task: %s", id)
	}
	data, newOffset, more, err := t.output.ReadSinceLimit(offset, maxBytes)
	if err != nil {
		return "", offset, false, err
	}
	if len(data) == 0 {
		return "", offset, more, nil
	}
	return string(data), newOffset, more, nil
}

func (m *Manager) PeekOrEmpty(id string, n int) string {
	s, _ := m.Peek(id, n)
	return s
}

func (m *Manager) Wait(ctx context.Context, id string, timeout time.Duration) (Info, error) {
	m.mu.Lock()
	t, ok := m.tasks[id]
	m.mu.Unlock()
	if !ok {
		return Info{}, fmt.Errorf("no such task: %s", id)
	}
	var timerC <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timerC = timer.C
	}
	select {
	case <-t.done:
	case <-timerC:
	case <-ctx.Done():
		return t.Info, ctx.Err()
	}
	return m.snapshot(t), nil
}

func (m *Manager) snapshot(t *task) Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	return t.Info
}

func (m *Manager) markKillCause(t *task, cause string) {
	if cause == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.killCause == "" {
		t.killCause = cause
	}
}

func (m *Manager) killCause(t *task) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return t.killCause
}

// --- helpers ---

func genID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func labelFromCommand(cmdLine string) string {
	cmdLine = strings.TrimSpace(cmdLine)
	if i := strings.IndexAny(cmdLine, " \t\n"); i > 0 {
		cmdLine = cmdLine[:i]
	}
	if i := strings.LastIndex(cmdLine, "/"); i >= 0 {
		cmdLine = cmdLine[i+1:]
	}
	if cmdLine == "" {
		return "shell"
	}
	return cmdLine
}

func FormatCompletion(info Info, killed bool, killCause, lastOutput string) string {
	duration := info.EndedAt.Sub(info.StartedAt).Round(time.Second)
	status := "completed"
	switch {
	case killed:
		status = "killed (" + killCause + ")"
	case info.ExitCode != 0:
		status = fmt.Sprintf("exited with code %d", info.ExitCode)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "<task_completion id=%q name=%q exit_code=%d duration=%q>\n",
		info.ID, info.Name, info.ExitCode, duration.String())
	fmt.Fprintf(&sb, "Background task %s.\n", status)
	if lastOutput != "" {
		sb.WriteString("--- last 20 lines ---\n")
		sb.WriteString(lastOutput)
		sb.WriteString("\n")
	} else {
		sb.WriteString("(no output)\n")
	}
	sb.WriteString("</task_completion>")
	return sb.String()
}
