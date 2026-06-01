// Package task provides a PTY-based session manager. Each session runs a
// command in a pseudo-terminal with buffered output, interactive input, and
// lifecycle management. The API mirrors tmux semantics: Create, List, Peek,
// Write, Kill, Wait.
package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

type Info struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	Command   string    `json:"command"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	ExitCode  int       `json:"exit_code"`
	State     State     `json:"state"`
	KillCause string    `json:"kill_cause,omitempty"`
}

type session struct {
	Info
	cmd       *exec.Cmd
	output    *OutputBuffer
	pty       *ptyHandle
	pumpDone  <-chan struct{}
	done      chan struct{}
	peekOff   int64
}

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
	onDone   func(Info)
	bufCap   int
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*session)}
}

func (m *Manager) SetOnDone(fn func(Info)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onDone = fn
}

// Create starts a shell command in a PTY session.
func (m *Manager) Create(workDir, cmdLine, name string, timeout time.Duration, env []string, outputFile string) (Info, error) {
	if strings.TrimSpace(cmdLine) == "" {
		return Info{}, errors.New("empty command")
	}
	c := ShellCommand(cmdLine)
	c.Dir = workDir
	if len(env) > 0 {
		c.Env = mergeEnv(os.Environ(), env)
	}
	return m.start(c, cmdLine, name, timeout, outputFile)
}

// CreateCmd starts a command with explicit binary and args in a PTY session.
func (m *Manager) CreateCmd(workDir, binary string, args []string, name string, timeout time.Duration, env []string, outputFile string) (Info, error) {
	c := exec.Command(binary, args...)
	c.Dir = workDir
	if len(env) > 0 {
		c.Env = mergeEnv(os.Environ(), env)
	}
	display := binary + " " + strings.Join(args, " ")
	return m.start(c, display, name, timeout, outputFile)
}

// mergeEnv merges override env vars into base, replacing any existing keys.
func mergeEnv(base, override []string) []string {
	overrideKeys := make(map[string]bool, len(override))
	for _, e := range override {
		if k, _, ok := strings.Cut(e, "="); ok {
			overrideKeys[k] = true
		}
	}
	result := make([]string, 0, len(base)+len(override))
	for _, e := range base {
		if k, _, ok := strings.Cut(e, "="); ok && overrideKeys[k] {
			continue
		}
		result = append(result, e)
	}
	return append(result, override...)
}

func (m *Manager) start(c *exec.Cmd, cmdDisplay, name string, timeout time.Duration, outputFile string) (Info, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if name == "" {
		name = labelFromCommand(cmdDisplay)
	}

	id, err := genID()
	if err != nil {
		return Info{}, err
	}

	buf, err := m.newBuffer(outputFile)
	if err != nil {
		return Info{}, err
	}

	p, err := startPTY(c)
	if err != nil {
		buf.Close()
		return Info{}, fmt.Errorf("start pty: %w", err)
	}

	pd := pumpOutput(p, buf)

	info := Info{
		ID:        id,
		Name:      name,
		Command:   cmdDisplay,
		PID:       c.Process.Pid,
		StartedAt: time.Now(),
		State:     StateRunning,
	}
	s := &session{Info: info, cmd: c, output: buf, pty: p, pumpDone: pd, done: make(chan struct{})}

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	go m.supervise(s, timeout)
	return info, nil
}

func (m *Manager) supervise(s *session, timeout time.Duration) {
	waitDone := make(chan error, 1)
	go func() { waitDone <- s.cmd.Wait() }()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var (
		waitErr   error
		killCause string
	)

	select {
	case err := <-waitDone:
		waitErr = err
	case <-timer.C:
		killCause = fmt.Sprintf("timeout after %s", timeout)
		m.setKillCause(s, killCause)
		m.forceKill(s)
		waitErr = <-waitDone
	}

	if recorded := m.getKillCause(s); recorded != "" {
		killCause = recorded
	}

	state, exitCode := StateCompleted, 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		switch {
		case errors.As(waitErr, &exitErr):
			exitCode = exitErr.ExitCode()
			if killCause != "" {
				state = StateKilled
			} else {
				state = StateFailed
			}
		default:
			exitCode = -1
			state = StateFailed
		}
	} else if killCause != "" {
		state = StateKilled
	}

	if s.pty != nil {
		s.pty.Close()
	}
	if s.pumpDone != nil {
		<-s.pumpDone
	}

	m.mu.Lock()
	s.EndedAt = time.Now()
	s.ExitCode = exitCode
	s.State = state
	s.KillCause = killCause
	infoCopy := s.Info
	m.mu.Unlock()

	s.output.Close()
	close(s.done)

	m.mu.Lock()
	fn := m.onDone
	m.mu.Unlock()
	if fn != nil {
		defer func() { _ = recover() }()
		fn(infoCopy)
	}
}

func (m *Manager) forceKill(s *session) {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = signalProcessGroup(s.cmd.Process.Pid, false)
	timer := time.NewTimer(killGrace)
	defer timer.Stop()
	select {
	case <-s.done:
		return
	case <-timer.C:
	}
	_ = signalProcessGroup(s.cmd.Process.Pid, true)
}

func (m *Manager) Kill(id string) error {
	m.mu.Lock()
	s := m.resolve(id)
	ok := s != nil
	if ok {
		select {
		case <-s.done:
		default:
			if s.KillCause == "" {
				s.KillCause = "killed by user"
			}
		}
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("no such session: %s", id)
	}
	select {
	case <-s.done:
		return nil
	default:
	}
	go m.forceKill(s)
	return nil
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	running := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		select {
		case <-s.done:
		default:
			running = append(running, s)
		}
	}
	m.mu.Unlock()

	for _, s := range running {
		m.setKillCause(s, "shutdown")
		if s.cmd != nil && s.cmd.Process != nil {
			_ = signalProcessGroup(s.cmd.Process.Pid, false)
		}
	}
	deadline := time.After(killGrace)
	for _, s := range running {
		select {
		case <-s.done:
		case <-deadline:
			if s.cmd != nil && s.cmd.Process != nil {
				_ = signalProcessGroup(s.cmd.Process.Pid, true)
			}
		}
	}
	finalDeadline := time.After(shutdownGrace)
	for _, s := range running {
		select {
		case <-s.done:
		case <-finalDeadline:
		}
	}
}

// --- query methods ---

func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.Info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

func (m *Manager) Get(id string) (Info, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.resolve(id)
	if s == nil {
		return Info{}, false
	}
	return s.Info, true
}

// resolve finds a session by ID first, then by name.
func (m *Manager) resolve(idOrName string) *session {
	if s, ok := m.sessions[idOrName]; ok {
		return s
	}
	for _, s := range m.sessions {
		if s.Name == idOrName {
			return s
		}
	}
	return nil
}

func (m *Manager) RunningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sessions {
		if s.State == StateRunning {
			n++
		}
	}
	return n
}

func (m *Manager) Done(id string) <-chan struct{} {
	m.mu.Lock()
	s := m.resolve(id)
	m.mu.Unlock()
	if s == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return s.done
}

func (m *Manager) Peek(id string, n int) (string, error) {
	m.mu.Lock()
	s := m.resolve(id)
	m.mu.Unlock()
	if s == nil {
		return "", fmt.Errorf("no such session: %s", id)
	}
	if n <= 0 {
		n = 30
	}
	return s.output.TailLines(n), nil
}

func (m *Manager) PeekOrEmpty(id string, n int) string {
	s, _ := m.Peek(id, n)
	return s
}

const defaultPeekNewMax int64 = 40 * 1024

func (m *Manager) PeekNew(id string, maxBytes int64) (string, bool, error) {
	m.mu.Lock()
	s := m.resolve(id)
	m.mu.Unlock()
	if s == nil {
		return "", false, fmt.Errorf("no such session: %s", id)
	}
	if maxBytes <= 0 {
		maxBytes = defaultPeekNewMax
	}

	m.mu.Lock()
	offset := s.peekOff
	m.mu.Unlock()

	data, newOff, more, err := s.output.ReadSinceLimit(offset, maxBytes)
	if err != nil {
		return "", false, err
	}

	m.mu.Lock()
	s.peekOff = newOff
	m.mu.Unlock()

	return string(data), more, nil
}

func (m *Manager) Write(id string, data []byte) error {
	m.mu.Lock()
	s := m.resolve(id)
	m.mu.Unlock()
	if s == nil {
		return fmt.Errorf("no such session: %s", id)
	}
	select {
	case <-s.done:
		return fmt.Errorf("session %s already finished", id)
	default:
	}
	if s.pty == nil {
		return fmt.Errorf("session %s has no PTY", id)
	}
	_, err := s.pty.Write(data)
	return err
}

func (m *Manager) Wait(ctx context.Context, id string, timeout time.Duration) (Info, error) {
	m.mu.Lock()
	s := m.resolve(id)
	m.mu.Unlock()
	if s == nil {
		return Info{}, fmt.Errorf("no such session: %s", id)
	}
	var timerC <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timerC = timer.C
	}
	select {
	case <-s.done:
	case <-timerC:
	case <-ctx.Done():
		return s.Info, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return s.Info, nil
}

// --- helpers ---

func (m *Manager) setKillCause(s *session, cause string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.KillCause == "" {
		s.KillCause = cause
	}
}

func (m *Manager) getKillCause(s *session) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return s.KillCause
}

func (m *Manager) bufferCap() int {
	if m.bufCap > 0 {
		return m.bufCap
	}
	return DefaultBufferCap
}

func (m *Manager) newBuffer(outputFile string) (*OutputBuffer, error) {
	cap := m.bufferCap()
	buf := &OutputBuffer{
		buf:       make([]byte, 0, min(cap, 64*1024)),
		cap:       cap,
		stripANSI: true,
	}
	if outputFile != "" {
		f, err := os.OpenFile(outputFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open output file: %w", err)
		}
		buf.file = f
	}
	return buf, nil
}

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

func FormatCompletion(info Info, lastOutput string) string {
	duration := info.EndedAt.Sub(info.StartedAt).Round(time.Second)
	status := "completed"
	switch {
	case info.State == StateKilled:
		status = "killed"
		if info.KillCause != "" {
			status += " (" + info.KillCause + ")"
		}
	case info.ExitCode != 0:
		status = fmt.Sprintf("exited with code %d", info.ExitCode)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "<session_completion id=%q name=%q exit_code=%d duration=%q>\n",
		info.ID, info.Name, info.ExitCode, duration.String())
	fmt.Fprintf(&sb, "Background session %s.\n", status)
	if lastOutput != "" {
		sb.WriteString("--- last 20 lines ---\n")
		sb.WriteString(lastOutput)
		sb.WriteString("\n")
	} else {
		sb.WriteString("(no output)\n")
	}
	sb.WriteString("</session_completion>")
	return sb.String()
}
