package tui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	bspinner "github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// This file owns render-mode detection, ANSI escape helpers, terminal
// hyperlinks, and the primary-buffer spinner. Hard rule: the REPL runs inside a
// PTY that can be forwarded remotely (e.g. via rem) to another agent like
// aider, so the renderer lives in the primary scrollback buffer only. No
// alternate screen, no absolute cursor positioning, no scroll-region tricks —
// and every transient UI element must eventually collapse into a static,
// newline-terminated line ("settled transcript" invariant) so the recorded
// stream never holds a dangling frame.

// ---------------------------------------------------------------------------
// Render mode
// ---------------------------------------------------------------------------

type RenderMode int

const (
	ModeInteractive RenderMode = iota
	ModeForwarded
)

func resolveRenderMode() RenderMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AISCAN_RENDER"))) {
	case "forwarded", "forward", "remote", "pipe":
		return ModeForwarded
	case "interactive", "tty", "local":
		return ModeInteractive
	}
	return ModeInteractive
}

// ---------------------------------------------------------------------------
// ANSI escape constants
// ---------------------------------------------------------------------------

const (
	syncBegin = "\x1b[?2026h"
	syncEnd   = "\x1b[?2026l"
	eraseLine = "\x1b[2K"
	carriage  = "\r"
	cursorUp  = "\x1b[1A"
)

// ---------------------------------------------------------------------------
// Synchronized output
// ---------------------------------------------------------------------------

func writeSynced(w io.Writer, fn func()) {
	if w == nil {
		return
	}
	fmt.Fprint(w, syncBegin)
	defer fmt.Fprint(w, syncEnd)
	fn()
}

// eraseLines clears n lines ending at the current cursor row.
func eraseLines(w io.Writer, n int) {
	if n <= 0 {
		return
	}
	fmt.Fprint(w, carriage+eraseLine)
	for i := 1; i < n; i++ {
		fmt.Fprint(w, cursorUp+eraseLine)
	}
}

// ---------------------------------------------------------------------------
// Spinner — bubbletea-based multi-line activity indicator
// ---------------------------------------------------------------------------

// spinnerSentinel is a placeholder embedded in live lines to mark where the
// animated spinner frame should be injected during render.
const spinnerSentinel = "\x00"

var defaultSpinnerType = bspinner.Spinner{
	Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	FPS:    90 * time.Millisecond,
}

// bubbletea messages for spinner control
type spinnerLabelMsg string
type spinnerLinesMsg []string
type spinnerQuitMsg struct{}

type spinnerModel struct {
	spin     bspinner.Model
	label    string
	lines    []string
	start    time.Time
	accent   string
	hint     string
	quitting bool
}

func (m spinnerModel) Init() tea.Cmd {
	return m.spin.Tick
}

func (m spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinnerQuitMsg:
		m.quitting = true
		return m, tea.Quit
	case spinnerLabelMsg:
		m.label = string(msg)
		return m, nil
	case spinnerLinesMsg:
		m.lines = msg
		return m, nil
	case bspinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m spinnerModel) View() string {
	if m.quitting {
		return ""
	}

	frame := m.accent + m.spin.View() + "\x1b[0m"
	var b strings.Builder

	if len(m.lines) > 0 {
		for i, line := range m.lines {
			b.WriteString(strings.Replace(line, spinnerSentinel, frame, 1))
			if i < len(m.lines)-1 {
				b.WriteByte('\n')
			}
		}
	} else {
		elapsed := ""
		if e := time.Since(m.start); e >= time.Second {
			elapsed = fmt.Sprintf(" · %.0fs", e.Seconds())
		}
		fmt.Fprintf(&b, "%s %s%s", frame, m.label, elapsed)
	}

	if m.hint != "" {
		fmt.Fprintf(&b, "\n\x1b[90m%s\x1b[0m", m.hint)
	}
	return b.String()
}

func (m spinnerModel) lineCount() int {
	n := 1
	if len(m.lines) > 0 {
		n = len(m.lines)
	}
	if m.hint != "" {
		n++
	}
	return n
}

// spinner wraps a bubbletea program to provide the Start/Stop/SetLines API.
// All methods are nil-safe and goroutine-safe.
type spinner struct {
	w      io.Writer
	accent string
	hint   string

	mu         sync.Mutex
	program    *tea.Program
	inputClose io.Closer
	running    bool
	done       chan struct{}
	lineCount  int // last known rendered line count for cleanup
}

func newSpinner(w io.Writer, accent string) *spinner {
	return &spinner{w: w, accent: accent}
}

func newSpinnerWithHint(w io.Writer, accent string) *spinner {
	return &spinner{
		w:      w,
		accent: accent,
		hint:   "Esc/Ctrl+C stop · Ctrl+O verbosity",
	}
}

func (s *spinner) Start(label string) {
	if s == nil || s.w == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		s.program.Send(spinnerLabelMsg(label))
		return
	}
	m := spinnerModel{
		spin:   bspinner.New(bspinner.WithSpinner(defaultSpinnerType)),
		label:  label,
		start:  time.Now(),
		accent: s.accent,
		hint:   s.hint,
	}
	s.lineCount = m.lineCount()
	pr, pw := io.Pipe()
	s.inputClose = pw
	s.program = tea.NewProgram(
		m,
		tea.WithOutput(s.w),
		tea.WithInput(pr),
		tea.WithoutSignalHandler(),
	)
	s.done = make(chan struct{})
	s.running = true
	go func() {
		defer close(s.done)
		s.program.Run()
	}()
}

func (s *spinner) SetLines(lines []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running && s.program != nil {
		copied := make([]string, len(lines))
		copy(copied, lines)
		n := len(copied)
		if n == 0 {
			n = 1
		}
		if s.hint != "" {
			n++
		}
		s.lineCount = n
		s.program.Send(spinnerLinesMsg(copied))
	}
}

func (s *spinner) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.program.Send(spinnerQuitMsg{})
	if s.inputClose != nil {
		s.inputClose.Close()
	}
	n := s.lineCount
	s.running = false
	s.lineCount = 0
	done := s.done
	s.mu.Unlock()
	<-done
	writeSynced(s.w, func() {
		eraseLines(s.w, n)
	})
}
