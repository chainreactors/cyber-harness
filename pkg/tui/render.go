package tui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
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

// RenderMode selects how aggressively the renderer decorates output.
type RenderMode int

const (
	// ModeInteractive: a human sits at a local TTY. Full firepower — truecolor
	// via lipgloss/glamour, single-line spinners, OSC 8 hyperlinks, and
	// synchronized-output flicker suppression.
	ModeInteractive RenderMode = iota
	// ModeForwarded: the PTY stream is consumed by a remote agent (aider).
	// Transient UI (spinners) is suppressed so the forwarded transcript stays a
	// clean line-oriented stream; OSC sequences degrade to no-ops in dumb
	// consumers and colors are kept (harmless — stripped or rendered downstream).
	ModeForwarded
)

// resolveRenderMode honors an explicit AISCAN_RENDER override so the rem
// forwarding path can opt into the degraded renderer without code changes.
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

// Inline ANSI escape helpers. All are line-local: they move within or rewrite
// the current row only, which keeps them safe over a forwarded PTY.
const (
	syncBegin = "\x1b[?2026h" // DCS 2026: begin synchronized output (flicker-free)
	syncEnd   = "\x1b[?2026l" // DCS 2026: end synchronized output
	eraseLine = "\x1b[2K"     // erase the entire current line
	carriage  = "\r"          // move to column 0 of the current row
)

// ---------------------------------------------------------------------------
// Synchronized output
// ---------------------------------------------------------------------------

// writeSynced wraps a burst of volatile writes in synchronized-output escape
// sequences so capable terminals paint them as a single frame (no flicker on
// spinner repaints). Forwarders/terminals that don't understand DCS 2026 ignore
// the unknown passthrough — graceful degradation, never corruption.
func writeSynced(w io.Writer, fn func()) {
	if w == nil {
		return
	}
	fmt.Fprint(w, syncBegin)
	defer fmt.Fprint(w, syncEnd)
	fn()
}

// ---------------------------------------------------------------------------
// Terminal hyperlinks (OSC 8)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Spinner — single-line primary-buffer activity indicator
// ---------------------------------------------------------------------------

const spinnerFrameInterval = 90 * time.Millisecond

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner is a single-line, primary-buffer activity indicator. It repaints the
// current row using carriage-return + line-erase (never absolute cursor moves),
// and Stop() always collapses the line so the recorded PTY transcript never
// retains a dangling transient frame. All methods are nil-safe.
type spinner struct {
	w      io.Writer
	accent string // ANSI color code for the frame ("" when color disabled)

	mu      sync.Mutex
	label   string
	start   time.Time
	stop    chan struct{}
	done    chan struct{}
	running bool
}

func newSpinner(w io.Writer, accent string) *spinner {
	return &spinner{w: w, accent: accent}
}

// Start begins (or retargets) the spinner. Calling Start while already running
// only refreshes the label in place — no second goroutine is spawned.
func (s *spinner) Start(label string) {
	if s == nil || s.w == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		s.label = label
		return
	}
	s.label = label
	s.start = time.Now()
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.running = true
	go s.tick()
}

func (s *spinner) tick() {
	defer close(s.done)
	t := time.NewTicker(spinnerFrameInterval)
	defer t.Stop()
	frame := 0
	for {
		s.render(frame)
		frame = (frame + 1) % len(spinnerFrames)
		select {
		case <-s.stop:
			return
		case <-t.C:
		}
	}
}

func (s *spinner) render(frame int) {
	if s == nil || s.w == nil {
		return
	}
	s.mu.Lock()
	label := s.label
	elapsed := ""
	if e := time.Since(s.start); e >= time.Second {
		elapsed = fmt.Sprintf(" · %.0fs", e.Seconds())
	}
	s.mu.Unlock()
	writeSynced(s.w, func() {
		fmt.Fprintf(s.w, "%s%s%s%s %s%s\x1b[0m",
			carriage, eraseLine, s.accent, spinnerFrames[frame], label, elapsed)
	})
}

// Stop collapses the spinner (erases its line) and joins the ticker goroutine.
// Safe to call when not running or on a nil receiver.
func (s *spinner) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	close(s.stop)
	s.running = false
	done := s.done
	s.mu.Unlock()
	<-done
	writeSynced(s.w, func() {
		fmt.Fprint(s.w, carriage+eraseLine)
	})
}
