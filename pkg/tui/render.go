package tui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/util"
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

// elapsedSentinel is replaced whenever a status line is rendered, so elapsed
// time stays transient instead of entering terminal history.
const elapsedSentinel = "\x01"

var defaultFrames = bspinner.Dot
var readlineFooterInterval = 100 * time.Millisecond

// LiveView manages transient status output. Both direct terminal rendering and
// the readline composer animate on a timer; the composer redraw stays inside
// readline so it does not replace the terminal's native scrollback.
type LiveView struct {
	w      io.Writer
	accent string // ANSI color for spinner frames

	mu       sync.Mutex
	lines    []string
	running  bool
	hidden   bool
	frame    string
	rendered int
	elapsed  time.Time
	stop     chan struct{}
	done     chan struct{}

	sink func(string) // event-driven readline footer
}

func NewLiveView(w io.Writer, accent string) *LiveView {
	return &LiveView{w: w, accent: accent}
}

// SetStatusSink renders the live line through an external inline-composer
// footer instead of writing cursor-control sequences directly to the terminal.
func (v *LiveView) SetStatusSink(sink func(string)) {
	if v == nil {
		return
	}
	v.mu.Lock()
	v.sink = sink
	v.mu.Unlock()
}

// EventDriven reports whether rendering is delegated to readline. This mode
// coalesces stream events and uses the dedicated composer refresh interval.
func (v *LiveView) EventDriven() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.sink != nil
}

func (v *LiveView) Update(lines []string) {
	v.update(lines, true)
}

// UpdateDeferred replaces the next animation frame without forcing an
// immediate terminal redraw. Readline stream deltas use this to coalesce many
// token events into the composer's 100ms refresh cadence.
func (v *LiveView) UpdateDeferred(lines []string) {
	v.update(lines, false)
}

func (v *LiveView) update(lines []string, render bool) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lines = make([]string, len(lines))
	copy(v.lines, lines)
	if render && v.running && !v.hidden {
		v.renderLocked(v.currentFrame())
	}
}

// SetElapsedStart controls the live duration placeholder used by status lines.
func (v *LiveView) SetElapsedStart(start time.Time) {
	if v == nil {
		return
	}
	v.mu.Lock()
	v.elapsed = start
	v.mu.Unlock()
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
	v.running = true
	v.frame = defaultFrames.Frames[0]
	v.renderLocked(v.frame)
	v.stop = make(chan struct{})
	v.done = make(chan struct{})
	interval := defaultFrames.FPS
	if v.sink != nil {
		// The composer interval is independent from other terminal renderers so
		// it can be tuned without changing tool/output animation globally.
		interval = readlineFooterInterval
	}
	go v.tick(interval)
}

func (v *LiveView) tick(interval time.Duration) {
	defer close(v.done)
	frames := defaultFrames.Frames
	t := time.NewTicker(interval)
	defer t.Stop()
	// Start already rendered frames[0]. Advance to the next frame on the first
	// tick so a 100ms refresh interval produces a visible change after 100ms,
	// rather than repainting the same frame and appearing to run at 200ms.
	idx := 0
	if len(frames) > 1 {
		idx = 1
	}
	for {
		select {
		case <-v.stop:
			return
		case <-t.C:
			v.render(frames[idx])
			idx = (idx + 1) % len(frames)
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
	if v.sink != nil {
		v.renderSinkLocked(frame)
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

	writeSynced(v.w, func() {
		eraseLines(v.w, prev)
		for i, line := range lines {
			replaced := v.expandLineLocked(line, frame)
			if i < len(lines)-1 {
				fmt.Fprintf(v.w, "%s\n", replaced)
			} else {
				fmt.Fprint(v.w, replaced)
			}
		}
	})

	v.rendered = len(lines)
}

func (v *LiveView) renderSinkLocked(frame string) {
	if len(v.lines) == 0 {
		v.sink("")
		return
	}
	lines := make([]string, 0, len(v.lines))
	for _, line := range v.lines {
		lines = append(lines, v.expandLineLocked(line, frame))
	}
	v.sink(strings.Join(lines, "\n"))
}

func (v *LiveView) expandLineLocked(line, frame string) string {
	marker := v.accent + frame + "\x1b[0m"
	line = strings.Replace(line, spinnerSentinel, marker, 1)
	if strings.Contains(line, elapsedSentinel) {
		elapsed := time.Duration(0)
		if !v.elapsed.IsZero() {
			elapsed = time.Since(v.elapsed)
		}
		line = strings.ReplaceAll(line, elapsedSentinel, util.FormatDuration(elapsed))
	}
	return line
}

func (v *LiveView) WithHidden(fn func()) {
	if v == nil {
		if fn != nil {
			fn()
		}
		return
	}
	if v.sink != nil {
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
	v.running = false
	v.hidden = false
	n := v.rendered
	v.rendered = 0
	sink := v.sink
	close(v.stop)
	done := v.done
	v.mu.Unlock()
	<-done
	if sink != nil {
		sink("")
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
