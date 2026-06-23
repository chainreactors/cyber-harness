package tui

import (
	"fmt"
	"io"
	"strings"

	"github.com/chainreactors/aiscan/core/output"
)

// StreamWriter manages token-by-token content streaming with paragraph-level
// markdown buffering and reasoning block tracking. It owns all cursor state
// for the stdout/stderr interleaving.
type StreamWriter struct {
	stdout    io.Writer
	stderr    io.Writer
	enabled   bool
	markdown  bool
	color     output.Color
	verbosity int

	printed    int    // content bytes flushed
	buf        string // paragraph buffer
	reasonPrt  int    // reasoning bytes flushed
	reasonFull string // cumulative reasoning text
	reasonOpen bool   // reasoning is being printed; flush remainder before content
	lineOpen   bool   // stdout cursor mid-line
	streamed   bool   // any content was streamed this turn
}

func NewStreamWriter(stdout, stderr io.Writer, enabled, markdown bool, color output.Color, verbosity int) *StreamWriter {
	return &StreamWriter{
		stdout:    stdout,
		stderr:    stderr,
		enabled:   enabled,
		markdown:  markdown,
		color:     color,
		verbosity: verbosity,
	}
}

// Delta processes a new token delta (content and/or reasoning).
func (w *StreamWriter) Delta(content, reasoning *string) {
	if !w.enabled || w.stdout == nil {
		return
	}

	// Reasoning: stream incrementally to stderr in dim. This avoids repainting
	// long wrapped lines in the live view, which terminals cannot erase reliably
	// without width-aware row accounting.
	if w.verbosity >= 2 && reasoning != nil {
		w.reasonFull = *reasoning
		if len(w.reasonFull) > w.reasonPrt {
			if !w.reasonOpen {
				w.EnsureNewline()
				w.reasonOpen = true
			}
			fmt.Fprint(w.stderr, w.color.Wrap(w.reasonFull[w.reasonPrt:], output.ANSIDim))
			w.reasonPrt = len(w.reasonFull)
		}
	}

	// Content: stream to stdout.
	text := ""
	if content != nil {
		text = *content
	}
	if len(text) <= w.printed {
		return
	}
	if w.printed == 0 && w.reasonOpen {
		w.closeReasoning()
	}
	delta := text[w.printed:]
	w.printed = len(text)
	w.streamed = true

	if !w.markdown {
		fmt.Fprint(w.stdout, delta)
		w.lineOpen = !strings.HasSuffix(text, "\n")
		return
	}
	w.buf += delta
	if pt := findParagraphFlushPoint(w.buf); pt > 0 {
		w.flushMarkdown(w.buf[:pt])
		w.buf = w.buf[pt:]
	}
}

// WouldPrintDelta reports whether Delta would emit visible output for this
// update. Role-only updates, hidden reasoning, and partial markdown chunks
// keep the live "thinking" view on screen.
func (w *StreamWriter) WouldPrintDelta(content, reasoning *string) bool {
	if w == nil || !w.enabled || w.stdout == nil {
		return false
	}
	if w.verbosity >= 2 && reasoning != nil {
		if len(*reasoning) > w.reasonPrt {
			return true
		}
	}
	if content == nil {
		return false
	}
	text := *content
	if len(text) <= w.printed {
		return false
	}
	if !w.markdown {
		return true
	}
	delta := text[w.printed:]
	return findParagraphFlushPoint(w.buf+delta) > 0
}

// Flush writes any buffered content and closes open reasoning blocks.
func (w *StreamWriter) Flush() {
	if w.buf != "" {
		w.flushMarkdown(w.buf)
		w.buf = ""
	}
	w.EnsureNewline()
	w.closeReasoning()
}

// EnsureNewline closes any mid-line stdout cursor.
func (w *StreamWriter) EnsureNewline() {
	if w.lineOpen && w.stdout != nil {
		fmt.Fprintln(w.stdout)
		w.lineOpen = false
	}
}

// NewTurn resets per-turn counters (called at EventTurnStart).
func (w *StreamWriter) NewTurn() {
	w.printed = 0
	w.reasonPrt = 0
	w.reasonFull = ""
}

// Reset clears all state (called at run start).
func (w *StreamWriter) Reset() {
	w.printed = 0
	w.buf = ""
	w.reasonPrt = 0
	w.reasonFull = ""
	w.reasonOpen = false
	w.lineOpen = false
	w.streamed = false
}

// Streamed returns true if any content was streamed this run.
func (w *StreamWriter) Streamed() bool { return w.streamed }

// ContentPrinted returns the number of content bytes already flushed.
func (w *StreamWriter) ContentPrinted() int { return w.printed }

// ReasoningPrinted returns the number of reasoning bytes already flushed.
func (w *StreamWriter) ReasoningPrinted() int { return w.reasonPrt }

// MarkStreamed marks the current content as rendered directly.
func (w *StreamWriter) MarkStreamed() {
	w.streamed = true
}

func (w *StreamWriter) flushMarkdown(text string) {
	if rendered := renderAgentMarkdown(text, true); rendered != "" {
		text = rendered
	}
	fmt.Fprint(w.stdout, text)
	if !strings.HasSuffix(text, "\n") {
		fmt.Fprintln(w.stdout)
	}
	w.lineOpen = false
}

func (w *StreamWriter) closeReasoning() {
	if !w.reasonOpen {
		return
	}
	if len(w.reasonFull) > w.reasonPrt {
		fmt.Fprintln(w.stderr, w.color.Wrap(w.reasonFull[w.reasonPrt:], output.ANSIDim))
		w.reasonPrt = len(w.reasonFull)
	} else if w.reasonFull != "" && !strings.HasSuffix(w.reasonFull, "\n") {
		fmt.Fprintln(w.stderr)
	}
	w.reasonOpen = false
}
