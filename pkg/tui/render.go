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
type LiveView struct {
	w      io.Writer
	accent string // ANSI color for spinner frames

	mu       sync.Mutex
	lines    []string
	running  bool
	rendered int
	stop     chan struct{}
	done     chan struct{}
}

func NewLiveView(w io.Writer, accent string) *LiveView {
	return &LiveView{w: w, accent: accent}
}

func (v *LiveView) Update(lines []string) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lines = make([]string, len(lines))
	copy(v.lines, lines)
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
	v.renderLocked(defaultFrames.Frames[0])
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
	lines := make([]string, len(v.lines))
	copy(lines, v.lines)
	prev := v.rendered

	if len(lines) == 0 {
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
	n := v.rendered
	v.rendered = 0
	done := v.done
	v.mu.Unlock()
	<-done
	if n > 0 {
		writeSynced(v.w, func() {
			eraseLines(v.w, n)
		})
	}
}
