package tui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	runewidth "github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

const (
	splitDefaultInputRows = 8  // prompt, multiline edits and completion menu
	splitMinRows          = 10 // don't split if terminal is too small
)

// SplitTerminal divides the terminal into three areas:
//
//	rows 1..scrollEnd   - scroll region for agent output
//	row  statusRow      - fixed status bar (thinking/tooling/talking)
//	rows inputRow..rows - fixed input area for readline
//
// The status bar always sits immediately above the fixed input viewport.
type SplitTerminal struct {
	mu  sync.Mutex
	raw io.Writer
	fd  int

	cols      int
	rows      int
	scrollEnd int
	statusRow int
	inputRow  int
	inputRows int // fixed input row budget
	active    bool

	// Tracked cursor position within the scroll region.
	outRow         int
	outCol         int
	outPendingWrap bool

	// Tracked cursor row inside the input area (0-based offset from inputRow).
	inputCurRow      int
	inputCurCol      int // 1-based column
	inputPendingWrap bool
	inputSaveRow     int
	inputSaveCol     int

	statusText string

	stopCh chan struct{}

	outputW *splitOutputWriter
	inputW  *splitInputWriter
}

func NewSplitTerminal(raw io.Writer, fd int) *SplitTerminal {
	cols, rows, _ := term.GetSize(fd)
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	st := &SplitTerminal{
		raw:  raw,
		fd:   fd,
		cols: cols,
		rows: rows,
	}
	st.applyInputRows(splitDefaultInputRows)
	st.outputW = &splitOutputWriter{st: st}
	st.inputW = &splitInputWriter{st: st, raw: raw}
	return st
}

// applyInputRows recalculates the three-area layout for the given input
// height. inputH is clamped to [1, rows/2].
func (st *SplitTerminal) applyInputRows(inputH int) {
	if inputH < 1 {
		inputH = 1
	}
	if max := st.rows / 2; inputH > max {
		inputH = max
	}
	reserve := inputH + 1 // +1 for the status bar row
	st.scrollEnd = st.rows - reserve
	if st.scrollEnd < 3 {
		st.scrollEnd = 3
	}
	st.statusRow = st.scrollEnd + 1
	st.inputRow = st.scrollEnd + 2
	st.inputRows = inputH
}

func (st *SplitTerminal) Setup() {
	st.mu.Lock()
	defer st.mu.Unlock()

	w := st.raw
	fmt.Fprint(w, "\x1b[?1049h") // alternate screen
	fmt.Fprint(w, "\x1b[2J")     // clear
	fmt.Fprintf(w, "\x1b[1;%dr", st.scrollEnd)
	st.outRow = 1
	st.outCol = 1
	st.outPendingWrap = false
	st.inputCurRow = 0
	st.inputCurCol = 1
	st.inputPendingWrap = false
	st.drawStatusContentLocked()
	st.moveInputCursorLocked()
	st.active = true

	st.stopCh = make(chan struct{})
	st.startResizeWatch()
}

func (st *SplitTerminal) Teardown() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.active {
		return
	}
	st.active = false
	st.stopResizeWatch()
	close(st.stopCh)
	w := st.raw
	fmt.Fprintf(w, "\x1b[1;%dr", st.rows)
	fmt.Fprint(w, "\x1b[?1049l")
}

// ---------------------------------------------------------------------------
// Status bar drawing
// ---------------------------------------------------------------------------

// drawStatusContentLocked positions the cursor at the status row, erases the
// line and writes the separator. Callers restore the input cursor explicitly.
func (st *SplitTerminal) drawStatusContentLocked() {
	w := st.raw
	text := st.statusText
	var line string
	if text == "" {
		line = "\x1b[2m" + strings.Repeat("─", st.cols) + "\x1b[0m"
	} else {
		tw := visibleWidth(text)
		trail := st.cols - 3 - tw
		if trail < 0 {
			trail = 0
		}
		line = "\x1b[2m──\x1b[0m" + text + " \x1b[2m" + strings.Repeat("─", trail) + "\x1b[0m"
	}
	fmt.Fprintf(w, "\x1b[%d;1H\x1b[2K%s", st.statusRow, line)
}

// drawStatusLocked is the convenience wrapper that restores the input cursor
// after drawing the status row.
func (st *SplitTerminal) drawStatusLocked() {
	st.drawStatusContentLocked()
	st.moveInputCursorLocked()
}

// ---------------------------------------------------------------------------
// Resize
// ---------------------------------------------------------------------------

func (st *SplitTerminal) handleResize() {
	cols, rows, err := term.GetSize(st.fd)
	if err != nil || cols <= 0 || rows <= 0 {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.active || (st.cols == cols && st.rows == rows) {
		return
	}
	st.cols = cols
	st.rows = rows
	st.applyInputRows(st.inputRows) // keep current input budget
	w := st.raw
	fmt.Fprint(w, syncBegin)
	fmt.Fprintf(w, "\x1b[1;%dr", st.scrollEnd)
	st.drawStatusContentLocked()
	st.clearInputAreaLocked()
	st.moveInputCursorLocked()
	fmt.Fprint(w, syncEnd)
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// OutputWriter returns a writer that routes output into the scroll region.
func (st *SplitTerminal) OutputWriter() io.Writer {
	return st.outputW
}

// InputWriter returns a writer for readline that serializes with output writes
// and clips terminal control sequences to the input area.
func (st *SplitTerminal) InputWriter() io.Writer {
	return st.inputW
}

// UpdateStatus replaces the status bar content.
func (st *SplitTerminal) UpdateStatus(text string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.statusText = text
	if !st.active {
		return
	}
	st.drawStatusLocked()
}

// ClearStatus resets the status bar to the default separator.
func (st *SplitTerminal) ClearStatus() {
	st.UpdateStatus("")
}

// Active reports whether the split layout is set up.
func (st *SplitTerminal) Active() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.active
}

// PrepareInputArea resets the input area to its default height and clears
// it for a new readline prompt.
func (st *SplitTerminal) PrepareInputArea() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.active {
		return
	}
	st.inputCurRow = 0
	st.inputCurCol = 1
	st.inputPendingWrap = false
	st.inputSaveRow = 0
	st.inputSaveCol = 1
	st.applyInputRows(splitDefaultInputRows)
	w := st.raw
	fmt.Fprint(w, syncBegin)
	fmt.Fprintf(w, "\x1b[1;%dr", st.scrollEnd)
	st.drawStatusContentLocked()
	st.clearInputAreaLocked()
	st.moveInputCursorLocked()
	fmt.Fprint(w, syncEnd)
}

// ---------------------------------------------------------------------------
// splitOutputWriter routes Write calls into the scroll region.
// ---------------------------------------------------------------------------

type splitOutputWriter struct {
	st *SplitTerminal
}

func (w *splitOutputWriter) Write(p []byte) (int, error) {
	w.st.mu.Lock()
	defer w.st.mu.Unlock()
	if !w.st.active {
		return w.st.raw.Write(p)
	}
	raw := w.st.raw
	fmt.Fprint(raw, syncBegin)
	fmt.Fprintf(raw, "\x1b[1;%dr", w.st.scrollEnd)
	w.st.moveOutputCursorLocked()
	err := w.st.writeOutputPaneLocked(p)
	w.st.moveInputCursorLocked()
	fmt.Fprint(raw, syncEnd)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// ---------------------------------------------------------------------------
// Output pane rendering
// ---------------------------------------------------------------------------

func (st *SplitTerminal) writeOutputPaneLocked(p []byte) error {
	for i := 0; i < len(p); {
		if p[i] == 0x1b {
			next := skipEscape(p, i)
			if next > len(p) {
				next = len(p)
			}
			seq := p[i:next]
			if len(seq) >= 3 && seq[1] == '[' {
				if err := st.writeOutputCSILocked(seq); err != nil {
					return err
				}
			} else if len(seq) >= 2 && seq[1] == ']' {
				if _, err := st.raw.Write(seq); err != nil {
					return err
				}
			}
			i = next
			continue
		}

		switch p[i] {
		case '\r':
			if _, err := io.WriteString(st.raw, "\r"); err != nil {
				return err
			}
			st.outCol = 1
			st.outPendingWrap = false
			i++
		case '\n':
			if err := st.outputLineFeedLocked(); err != nil {
				return err
			}
			i++
		case '\t':
			spaces := 4 - ((st.outCol - 1) % 4)
			for ; spaces > 0; spaces-- {
				if err := st.writeOutputRuneLocked(' '); err != nil {
					return err
				}
			}
			i++
		default:
			if p[i] < 0x20 || p[i] == 0x7f {
				i++
				continue
			}
			r, size := utf8.DecodeRune(p[i:])
			if r == utf8.RuneError && size == 1 {
				i++
				continue
			}
			if err := st.writeOutputRuneLocked(r); err != nil {
				return err
			}
			i += size
		}
	}
	return nil
}

func (st *SplitTerminal) writeOutputCSILocked(seq []byte) error {
	final := seq[len(seq)-1]
	params := parseCSIParams(seq)
	param := func(idx, def int) int {
		if idx >= len(params) || params[idx] <= 0 {
			return def
		}
		return params[idx]
	}

	switch final {
	case 'm':
		_, err := st.raw.Write(seq)
		return err
	case 'A':
		st.outPendingWrap = false
		st.outRow -= param(0, 1)
		st.moveOutputCursorLocked()
	case 'B':
		st.outPendingWrap = false
		st.outRow += param(0, 1)
		st.moveOutputCursorLocked()
	case 'C':
		st.outPendingWrap = false
		st.outCol += param(0, 1)
		st.moveOutputCursorLocked()
	case 'D':
		st.outPendingWrap = false
		st.outCol -= param(0, 1)
		st.moveOutputCursorLocked()
	case 'E':
		st.outPendingWrap = false
		st.outRow += param(0, 1)
		st.outCol = 1
		st.moveOutputCursorLocked()
	case 'F':
		st.outPendingWrap = false
		st.outRow -= param(0, 1)
		st.outCol = 1
		st.moveOutputCursorLocked()
	case 'G':
		st.outPendingWrap = false
		st.outCol = param(0, 1)
		st.moveOutputCursorLocked()
	case 'H', 'f':
		st.outPendingWrap = false
		st.outRow = param(0, 1)
		st.outCol = param(1, 1)
		st.moveOutputCursorLocked()
	case 'J':
		st.clearOutputScreenLocked()
	case 'K':
		_, err := st.raw.Write(seq)
		return err
	case 'r':
		return nil
	default:
		return nil
	}
	return nil
}

func (st *SplitTerminal) writeOutputRuneLocked(r rune) error {
	width := runewidth.RuneWidth(r)
	if width < 0 {
		width = 0
	}
	if width > 0 {
		if st.outPendingWrap {
			if err := st.outputLineFeedLocked(); err != nil {
				return err
			}
		}
		if st.outCol+width-1 > st.cols {
			if err := st.outputLineFeedLocked(); err != nil {
				return err
			}
		}
	}
	if _, err := io.WriteString(st.raw, string(r)); err != nil {
		return err
	}
	st.outCol += width
	if width > 0 && st.outCol > st.cols {
		st.outCol = st.cols
		st.outPendingWrap = true
	}
	return nil
}

func (st *SplitTerminal) outputLineFeedLocked() error {
	if _, err := io.WriteString(st.raw, "\r\n"); err != nil {
		return err
	}
	st.outCol = 1
	st.outPendingWrap = false
	if st.outRow < st.scrollEnd {
		st.outRow++
	}
	return nil
}

func (st *SplitTerminal) moveOutputCursorLocked() {
	if st.outRow < 1 {
		st.outRow = 1
	}
	if st.outRow > st.scrollEnd {
		st.outRow = st.scrollEnd
	}
	if st.outCol < 1 {
		st.outCol = 1
	}
	if st.outCol > st.cols {
		st.outCol = st.cols
	}
	fmt.Fprintf(st.raw, "\x1b[%d;%dH", st.outRow, st.outCol)
}

func (st *SplitTerminal) clearOutputScreenLocked() {
	curRow, curCol, pendingWrap := st.outRow, st.outCol, st.outPendingWrap
	for row := 1; row <= st.scrollEnd; row++ {
		fmt.Fprintf(st.raw, "\x1b[%d;1H\x1b[2K", row)
	}
	st.outRow, st.outCol = curRow, curCol
	st.outPendingWrap = pendingWrap
	st.moveOutputCursorLocked()
}

// ---------------------------------------------------------------------------
// splitInputWriter serializes readline writes into the input area.
// ---------------------------------------------------------------------------

type splitInputWriter struct {
	st  *SplitTerminal
	raw io.Writer
}

func (w *splitInputWriter) Write(p []byte) (int, error) {
	w.st.mu.Lock()
	defer w.st.mu.Unlock()
	if !w.st.active {
		return w.raw.Write(p)
	}
	raw := w.st.raw
	fmt.Fprint(raw, syncBegin)
	w.st.moveInputCursorLocked()
	err := w.st.writeInputPaneLocked(p)
	fmt.Fprint(raw, syncEnd)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// ---------------------------------------------------------------------------
// Input pane rendering
// ---------------------------------------------------------------------------

func (st *SplitTerminal) writeInputPaneLocked(p []byte) error {
	for i := 0; i < len(p); {
		if p[i] == 0x1b {
			next := skipEscape(p, i)
			if next > len(p) {
				next = len(p)
			}
			seq := p[i:next]
			if len(seq) >= 3 && seq[1] == '[' {
				if err := st.writeInputCSILocked(seq); err != nil {
					return err
				}
			} else if err := st.writeInputEscapeLocked(seq); err != nil {
				return err
			}
			i = next
			continue
		}

		switch p[i] {
		case '\r':
			st.inputCurCol = 1
			st.inputPendingWrap = false
			st.moveInputCursorLocked()
			i++
		case '\n':
			st.inputLineFeedLocked()
			i++
		case '\b':
			if st.inputCurCol > 1 {
				st.inputCurCol--
				st.inputPendingWrap = false
				st.moveInputCursorLocked()
			}
			i++
		case '\t':
			spaces := 4 - ((st.inputCurCol - 1) % 4)
			for ; spaces > 0; spaces-- {
				if err := st.writeInputRuneLocked(' '); err != nil {
					return err
				}
			}
			i++
		default:
			if p[i] < 0x20 || p[i] == 0x7f {
				i++
				continue
			}
			r, size := utf8.DecodeRune(p[i:])
			if r == utf8.RuneError && size == 1 {
				i++
				continue
			}
			if err := st.writeInputRuneLocked(r); err != nil {
				return err
			}
			i += size
		}
	}
	return nil
}

func (st *SplitTerminal) writeInputEscapeLocked(seq []byte) error {
	if len(seq) < 2 {
		return nil
	}
	switch seq[1] {
	case '7':
		st.inputSaveRow = st.inputCurRow
		st.inputSaveCol = st.inputCurCol
		return nil
	case '8':
		st.inputCurRow = st.inputSaveRow
		st.inputCurCol = st.inputSaveCol
		st.inputPendingWrap = false
		st.moveInputCursorLocked()
		return nil
	case ']':
		_, err := st.raw.Write(seq) // OSC, e.g. terminal title.
		return err
	case '(', ')', '*', '+':
		_, err := st.raw.Write(seq) // Charset selection.
		return err
	default:
		return nil
	}
}

func (st *SplitTerminal) writeInputCSILocked(seq []byte) error {
	final := seq[len(seq)-1]
	params := parseCSIParams(seq)
	param := func(idx, def int) int {
		if idx >= len(params) || params[idx] <= 0 {
			return def
		}
		return params[idx]
	}

	switch final {
	case 'A':
		st.inputPendingWrap = false
		st.inputCurRow -= param(0, 1)
		st.moveInputCursorLocked()
	case 'B':
		st.inputPendingWrap = false
		st.inputCurRow += param(0, 1)
		st.moveInputCursorLocked()
	case 'C':
		st.inputPendingWrap = false
		st.inputCurCol += param(0, 1)
		st.moveInputCursorLocked()
	case 'D':
		st.inputPendingWrap = false
		st.inputCurCol -= param(0, 1)
		st.moveInputCursorLocked()
	case 'E':
		st.inputPendingWrap = false
		st.inputCurRow += param(0, 1)
		st.inputCurCol = 1
		st.moveInputCursorLocked()
	case 'F':
		st.inputPendingWrap = false
		st.inputCurRow -= param(0, 1)
		st.inputCurCol = 1
		st.moveInputCursorLocked()
	case 'G':
		st.inputPendingWrap = false
		st.inputCurCol = param(0, 1)
		st.moveInputCursorLocked()
	case 'H', 'f':
		st.inputPendingWrap = false
		st.inputCurRow = param(0, 1) - 1
		st.inputCurCol = param(1, 1)
		st.moveInputCursorLocked()
	case 'J':
		mode := 0
		if len(params) > 0 {
			mode = params[0]
		}
		st.clearInputScreenLocked(mode)
	case 'K':
		_, err := st.raw.Write(seq)
		return err
	case 's':
		st.inputSaveRow = st.inputCurRow
		st.inputSaveCol = st.inputCurCol
	case 'u':
		st.inputCurRow = st.inputSaveRow
		st.inputCurCol = st.inputSaveCol
		st.inputPendingWrap = false
		st.moveInputCursorLocked()
	case 'r':
		// Never let readline alter the split terminal scroll margins.
		return nil
	default:
		_, err := st.raw.Write(seq)
		return err
	}
	return nil
}

func (st *SplitTerminal) writeInputRuneLocked(r rune) error {
	width := runewidth.RuneWidth(r)
	if width < 0 {
		width = 0
	}
	if width > 0 {
		if st.inputPendingWrap {
			st.inputLineFeedLocked()
		}
		if st.inputCurCol+width-1 > st.cols {
			st.inputLineFeedLocked()
		}
	}
	if st.inputCurRow >= st.inputRows {
		return nil
	}
	if _, err := io.WriteString(st.raw, string(r)); err != nil {
		return err
	}
	st.inputCurCol += width
	if width > 0 && st.inputCurCol > st.cols {
		st.inputCurCol = st.cols
		st.inputPendingWrap = true
	}
	return nil
}

func (st *SplitTerminal) inputLineFeedLocked() {
	st.inputCurCol = 1
	st.inputPendingWrap = false
	if st.inputCurRow < st.inputRows-1 {
		st.inputCurRow++
	}
	st.moveInputCursorLocked()
}

func (st *SplitTerminal) moveInputCursorLocked() {
	if st.inputCurRow < 0 {
		st.inputCurRow = 0
	}
	if st.inputCurRow >= st.inputRows {
		st.inputCurRow = st.inputRows - 1
	}
	if st.inputCurCol < 1 {
		st.inputCurCol = 1
	}
	if st.inputCurCol > st.cols {
		st.inputCurCol = st.cols
	}
	fmt.Fprintf(st.raw, "\x1b[%d;%dH", st.inputRow+st.inputCurRow, st.inputCurCol)
}

func (st *SplitTerminal) clearInputAreaLocked() {
	curRow, curCol, pendingWrap := st.inputCurRow, st.inputCurCol, st.inputPendingWrap
	for row := st.inputRow; row <= st.rows; row++ {
		fmt.Fprintf(st.raw, "\x1b[%d;1H\x1b[2K", row)
	}
	st.inputCurRow, st.inputCurCol = curRow, curCol
	st.inputPendingWrap = pendingWrap
	st.moveInputCursorLocked()
}

func (st *SplitTerminal) clearInputScreenLocked(mode int) {
	st.moveInputCursorLocked()
	switch mode {
	case 1:
		for row := st.inputRow; row < st.inputRow+st.inputCurRow; row++ {
			fmt.Fprintf(st.raw, "\x1b[%d;1H\x1b[2K", row)
		}
		st.moveInputCursorLocked()
		fmt.Fprint(st.raw, "\x1b[1K")
	case 2, 3:
		st.clearInputAreaLocked()
	default:
		fmt.Fprint(st.raw, "\x1b[0K")
		for row := st.inputRow + st.inputCurRow + 1; row <= st.rows; row++ {
			fmt.Fprintf(st.raw, "\x1b[%d;1H\x1b[2K", row)
		}
		st.moveInputCursorLocked()
	}
}

func parseCSIParams(seq []byte) []int {
	if len(seq) < 3 || seq[0] != 0x1b || seq[1] != '[' {
		return nil
	}
	body := seq[2 : len(seq)-1]
	params := make([]int, 0, 2)
	value := 0
	hasValue := false
	hasParam := false
	for _, b := range body {
		switch {
		case b >= '0' && b <= '9':
			value = value*10 + int(b-'0')
			hasValue = true
			hasParam = true
		case b == ';':
			params = append(params, value)
			value = 0
			hasValue = false
			hasParam = true
		case b == '?' || b == '>' || b == '=' || b == ' ':
			continue
		default:
			continue
		}
	}
	if hasValue || hasParam {
		params = append(params, value)
	}
	return params
}

func skipEscape(p []byte, i int) int {
	if i+1 >= len(p) {
		return i + 1
	}
	switch p[i+1] {
	case '[': // CSI sequence
		j := i + 2
		for j < len(p) && (p[j] < 0x40 || p[j] > 0x7e) {
			j++
		}
		if j < len(p) {
			j++
		}
		return j
	case ']': // OSC sequence
		j := i + 2
		for j < len(p) {
			if p[j] == 0x07 {
				return j + 1
			}
			if p[j] == 0x1b && j+1 < len(p) && p[j+1] == '\\' {
				return j + 2
			}
			j++
		}
		return j
	default:
		return i + 2
	}
}

// splitEnabled reports whether the terminal supports the split layout.
func splitEnabled(fd int, mode RenderMode) bool {
	if mode != ModeInteractive {
		return false
	}
	if !term.IsTerminal(fd) {
		return false
	}
	if os.Getenv("AISCAN_SPLIT") == "0" {
		return false
	}
	_, rows, err := term.GetSize(fd)
	if err != nil || rows < splitMinRows {
		return false
	}
	return true
}
