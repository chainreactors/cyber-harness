package tui

import (
	"io"
	"strings"
	"sync"

	"github.com/chainreactors/tui/readline"
)

// readlineConsoleBridge implements Claude Code's non-fullscreen rendering
// model: permanent output replaces the current prompt and then readline
// redraws the editor below it. The terminal remains on its primary screen and
// owns scrollback; no scroll margins or mouse reporting are used.
type readlineConsoleBridge struct {
	mu sync.Mutex
	// renderMu serializes prompt commits and async redraws; readline display
	// state is intentionally single-writer even though agent events are async.
	renderMu sync.Mutex
	raw      io.Writer
	active   func() bool
	pending  strings.Builder
	// version tracks status changes across Readline() boundaries. A status that
	// arrives during prompt startup is redrawn only after coordinates are ready.
	status           string
	version          uint64
	displayedVersion uint64
	ready            bool
	commit           func(string) error
	redraw           func()
}

// newReadlineConsoleBridge binds permanent output and transient status updates
// to one readline shell without taking ownership of terminal scrollback.
func newReadlineConsoleBridge(shell *readline.Shell, raw io.Writer, active func() bool) *readlineConsoleBridge {
	b := &readlineConsoleBridge{raw: raw, active: active}
	if shell != nil {
		b.commit = func(text string) error {
			_, err := shell.PrintTransientf("%s", text)
			return err
		}
		b.redraw = shell.RefreshPrimaryWithoutAutocomplete
	}
	return b
}

// Write commits newline-complete output above the active prompt. Incomplete
// fragments stay buffered so token deltas are not turned into separate lines.
func (b *readlineConsoleBridge) Write(p []byte) (int, error) {
	if b == nil {
		return len(p), nil
	}
	b.mu.Lock()
	active := b.active != nil && b.active() && b.commit != nil
	if !active {
		pending := b.pending.String()
		b.pending.Reset()
		raw := b.raw
		b.mu.Unlock()
		if raw == nil {
			return len(p), nil
		}
		b.renderMu.Lock()
		defer b.renderMu.Unlock()
		if pending != "" {
			if _, err := io.WriteString(raw, pending); err != nil {
				return 0, err
			}
		}
		_, err := raw.Write(p)
		return len(p), err
	}

	b.pending.Write(p)
	text := b.pending.String()
	lastNL := strings.LastIndexByte(text, '\n')
	if lastNL < 0 {
		b.mu.Unlock()
		return len(p), nil
	}

	complete := strings.ReplaceAll(text[:lastNL], "\r\n", "\n")
	complete = strings.TrimSuffix(complete, "\r")
	remainder := text[lastNL+1:]
	b.pending.Reset()
	b.pending.WriteString(remainder)
	commit := b.commit
	b.mu.Unlock()

	b.renderMu.Lock()
	defer b.renderMu.Unlock()
	if err := commit(complete); err != nil {
		return len(p), err
	}
	return len(p), nil
}

// UpdateStatus stores the latest shared thinking/talking/tooling row and
// redraws it only while readline has valid coordinates for the active prompt.
func (b *readlineConsoleBridge) UpdateStatus(text string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.status = text
	b.version++
	redraw := b.redraw
	active := redraw != nil && b.ready && b.active != nil && b.active()
	b.mu.Unlock()
	if active {
		b.renderMu.Lock()
		redraw()
		b.renderMu.Unlock()
	}
}

// Status returns the current composer status and records that the primary
// prompt has observed this version.
func (b *readlineConsoleBridge) Status() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.displayedVersion = b.version
	return b.status
}

// SetReady brackets one Readline() lifecycle. Pending status changes are
// replayed only after the first full display refresh has established offsets.
func (b *readlineConsoleBridge) SetReady(ready bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.ready = ready
	redraw := b.redraw
	shouldRedraw := ready && redraw != nil && b.displayedVersion != b.version
	b.mu.Unlock()
	if shouldRedraw {
		b.renderMu.Lock()
		redraw()
		b.renderMu.Unlock()
	}
}
