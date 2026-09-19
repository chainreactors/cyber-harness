package console

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/muesli/termenv"

	"github.com/chainreactors/cyber/core/output"
)

// ---------------------------------------------------------------------------
// Paragraph boundary detection for streaming markdown
// ---------------------------------------------------------------------------

// findParagraphFlushPoint returns the byte offset up to which buf can be
// safely rendered as complete markdown paragraphs. It detects blank-line
// boundaries while skipping over fenced code blocks (``` / ~~~). Returns -1
// if no safe flush point exists yet.
func findParagraphFlushPoint(buf string) int {
	inFence := false
	flushPoint := -1
	pos := 0

	for pos < len(buf) {
		nl := strings.IndexByte(buf[pos:], '\n')
		if nl < 0 {
			break
		}
		line := buf[pos : pos+nl]
		pos += nl + 1

		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}

		if !inFence && trimmed == "" {
			flushPoint = pos
		}
	}

	return flushPoint
}

// ---------------------------------------------------------------------------
// Syntax highlighting for read tool results
// ---------------------------------------------------------------------------

var lineNumberRe = regexp.MustCompile(`^(\d+)\t`)

// highlightReadResult applies chroma syntax highlighting to read tool output
// lines. path is used for lexer detection. Lines in "N\tcontent" format get
// dim line numbers with highlighted content; other lines pass through.
func highlightReadResult(path string, lines []string, color output.Color) []string {
	if !color.Enabled || path == "" || len(lines) == 0 {
		return lines
	}

	lexer := lexers.Match(path)
	if lexer == nil {
		return lines
	}
	lexer = chroma.Coalesce(lexer)

	type lineInfo struct {
		number  string
		content string
	}
	infos := make([]lineInfo, 0, len(lines))
	contentLines := make([]string, 0, len(lines))

	for _, line := range lines {
		if m := lineNumberRe.FindStringSubmatch(line); m != nil {
			infos = append(infos, lineInfo{number: m[1], content: line[len(m[0]):]})
			contentLines = append(contentLines, line[len(m[0]):])
		} else {
			infos = append(infos, lineInfo{number: "", content: line})
			contentLines = append(contentLines, "")
		}
	}

	source := strings.Join(contentLines, "\n")

	formatter := formatters.Get(selectChromaFormatter())
	if formatter == nil {
		formatter = formatters.Fallback
	}

	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}

	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		return lines
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return lines
	}

	highlighted := strings.Split(buf.String(), "\n")

	dim := color.Code(output.ANSIDim)
	reset := color.Code(output.ANSIReset)
	result := make([]string, 0, len(infos))

	for i, info := range infos {
		if info.number == "" {
			result = append(result, info.content)
			continue
		}
		hl := ""
		if i < len(highlighted) {
			hl = highlighted[i]
		}
		result = append(result, fmt.Sprintf("%s%s\t%s%s", dim, info.number, reset, hl))
	}

	return result
}

func selectChromaFormatter() string {
	switch termenv.ColorProfile() {
	case termenv.TrueColor:
		return "terminal16m"
	case termenv.ANSI256:
		return "terminal256"
	default:
		return "terminal256"
	}
}
