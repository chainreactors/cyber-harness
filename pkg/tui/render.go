package tui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	bspinner "github.com/charmbracelet/bubbles/spinner"
)

// ---------------------------------------------------------------------------
// Render mode
// ---------------------------------------------------------------------------

type RenderMode int

const (
	ModeInteractive RenderMode = iota
	ModeStatic
	ModeForwarded
)

func resolveRenderMode() RenderMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AISCAN_RENDER"))) {
	case "static", "plain", "noninteractive", "non-interactive", "off":
		return ModeStatic
	case "forwarded", "forward", "remote", "pipe":
		return ModeForwarded
	case "interactive", "tty", "local":
		return ModeInteractive
	}
	return ModeInteractive
}

// ---------------------------------------------------------------------------
// ANSI primitives
// ---------------------------------------------------------------------------

const (
	syncBegin = "\x1b[?2026h"
	syncEnd   = "\x1b[?2026l"
	eraseLine = "\x1b[2K"
	carriage  = "\r"
	cursorUp  = "\x1b[1A"
)

func writeSynced(w io.Writer, fn func()) {
	if w == nil {
		return
	}
	fmt.Fprint(w, syncBegin)
	defer fmt.Fprint(w, syncEnd)
	fn()
}

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
// LiveView — generic transient multi-line region
// ---------------------------------------------------------------------------

// spinnerSentinel marks where the animated frame should be injected.
const spinnerSentinel = "\x00"

var defaultFrames = bspinner.Dot

// LiveView manages a transient, animated region on the terminal. Lines
// containing spinnerSentinel get the current animation frame injected on each
// tick. Stop erases the region cleanly.
//
// When split is non-nil the view renders into the split terminal's status bar
// instead of using inline cursor-up/erase tricks.
type LiveView struct {
	w      io.Writer
	accent string // ANSI color for spinner frames

	mu       sync.Mutex
	lines    []string
	running  bool
	hidden   bool
	frame    string
	rendered int
	stop     chan struct{}
	done     chan struct{}

	split *SplitTerminal // set once; safe to read without mu
}

func NewLiveView(w io.Writer, accent string) *LiveView {
	return &LiveView{w: w, accent: accent}
}

// SetSplitTerminal puts the view into split mode: status is rendered to the
// split terminal's fixed status bar instead of inline. Must be called before
// Start and never changed afterwards.
func (v *LiveView) SetSplitTerminal(st *SplitTerminal) {
	if v == nil {
		return
	}
	v.split = st
	// Redirect the fallback writer into the scroll region so any code
	// path that writes to v.w directly can never leak into the raw
	// terminal (which would appear in the input area).
	v.w = st.OutputWriter()
}

func (v *LiveView) Update(lines []string) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lines = make([]string, len(lines))
	copy(v.lines, lines)
	if v.running && !v.hidden {
		v.renderLocked(v.currentFrame())
	}
}

func (v *LiveView) Start() {
	if v == nil || v.w == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.running {
		return
	}
	v.stop = make(chan struct{})
	v.done = make(chan struct{})
	v.running = true
	v.frame = defaultFrames.Frames[0]
	v.renderLocked(v.frame)
	go v.tick()
}

func (v *LiveView) tick() {
	defer close(v.done)
	frames := defaultFrames.Frames
	t := time.NewTicker(defaultFrames.FPS)
	defer t.Stop()
	idx := 0
	for {
		v.render(frames[idx])
		idx = (idx + 1) % len(frames)
		select {
		case <-v.stop:
			return
		case <-t.C:
		}
	}
}

func (v *LiveView) render(frame string) {
	v.mu.Lock()
	v.renderLocked(frame)
	v.mu.Unlock()
}

func (v *LiveView) renderLocked(frame string) {
	v.frame = frame
	if v.split != nil {
		v.renderSplitLocked(frame)
		return
	}
	if v.hidden {
		return
	}
	lines := make([]string, len(v.lines))
	copy(lines, v.lines)
	prev := v.rendered

	if len(lines) == 0 {
		if prev > 0 {
			writeSynced(v.w, func() {
				eraseLines(v.w, prev)
			})
			v.rendered = 0
		}
		return
	}

	marker := v.accent + frame + "\x1b[0m"
	writeSynced(v.w, func() {
		eraseLines(v.w, prev)
		for i, line := range lines {
			replaced := strings.Replace(line, spinnerSentinel, marker, 1)
			if i < len(lines)-1 {
				fmt.Fprintf(v.w, "%s\n", replaced)
			} else {
				fmt.Fprint(v.w, replaced)
			}
		}
	})

	v.rendered = len(lines)
}

// renderSplitLocked renders the first status line to the split terminal's
// status bar. Tool-progress lines are omitted (they appear permanently in
// the output area when completed).
func (v *LiveView) renderSplitLocked(frame string) {
	lines := v.lines
	if len(lines) == 0 {
		v.split.UpdateStatus("")
		return
	}
	marker := v.accent + frame + "\x1b[0m"
	text := strings.Replace(lines[0], spinnerSentinel, marker, 1)
	v.split.UpdateStatus(" " + text)
}

func (v *LiveView) WithHidden(fn func()) {
	if v == nil {
		if fn != nil {
			fn()
		}
		return
	}
	// In split mode output and status areas don't overlap; no need to
	// hide the status bar while writing content to the scroll region.
	if v.split != nil {
		if fn != nil {
			fn()
		}
		return
	}
	v.mu.Lock()
	if !v.running {
		v.mu.Unlock()
		if fn != nil {
			fn()
		}
		return
	}
	if v.rendered > 0 {
		writeSynced(v.w, func() {
			eraseLines(v.w, v.rendered)
		})
		v.rendered = 0
	}
	v.hidden = true
	v.mu.Unlock()

	if fn != nil {
		fn()
	}

	v.mu.Lock()
	v.hidden = false
	if v.running {
		v.renderLocked(v.currentFrame())
	}
	v.mu.Unlock()
}

func (v *LiveView) Stop() {
	if v == nil {
		return
	}
	v.mu.Lock()
	if !v.running {
		v.mu.Unlock()
		return
	}
	close(v.stop)
	v.running = false
	v.hidden = false
	n := v.rendered
	v.rendered = 0
	done := v.done
	isSplit := v.split != nil
	v.mu.Unlock()
	<-done
	if isSplit {
		v.split.ClearStatus()
		return
	}
	if n > 0 {
		writeSynced(v.w, func() {
			eraseLines(v.w, n)
		})
	}
}

func (v *LiveView) currentFrame() string {
	if v.frame != "" {
		return v.frame
	}
	return defaultFrames.Frames[0]
}
