package truncate

import (
	"strings"
	"unicode/utf8"

	"github.com/chainreactors/aiscan/pkg/util"
)

// ── Tier 1: 通用工具结果 (bash/read/grep/find/ls/inbox/agent result) ──

const (
	DefaultMaxLines = 2000
	DefaultMaxBytes = 50 * 1024 // 50KB
)

// ── Tier 2: 富内容 (网页/浏览器, 转 markdown 后体积更大) ──

const MaxContentLength = 100_000

// ── Tier 3: 网络 I/O 安全上限 (防 OOM) ──

const (
	MaxFetchBody    = 10 << 20 // 10MB
	MaxResponseBody = 1 << 20  // 1MB
)

// ── 结构性限制 (数量/大小) ──

const (
	GrepMaxLineLength = 500
	MaxGlobResults    = 500
	MaxImageSize      = 20 << 20 // 20MB
)

type Strategy string

const (
	ByLines Strategy = "lines"
	ByBytes Strategy = "bytes"
	None    Strategy = ""
)

type Result struct {
	Content               string
	Truncated             bool
	TruncatedBy           Strategy
	TotalLines            int
	TotalBytes            int
	OutputLines           int
	OutputBytes           int
	FirstLineExceedsLimit bool
	MaxLines              int
	MaxBytes              int
}

type Options struct {
	MaxLines int
	MaxBytes int
}

func (o Options) maxLines() int {
	if o.MaxLines > 0 {
		return o.MaxLines
	}
	return DefaultMaxLines
}

func (o Options) maxBytes() int {
	if o.MaxBytes > 0 {
		return o.MaxBytes
	}
	return DefaultMaxBytes
}

func splitLines(content string) []string {
	if len(content) == 0 {
		return nil
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" && strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Head keeps the first N lines / N bytes. Never returns partial lines.
// If the first line alone exceeds MaxBytes, returns empty content with
// FirstLineExceedsLimit=true.
func Head(content string, opts Options) Result {
	maxL := opts.maxLines()
	maxB := opts.maxBytes()
	totalBytes := len(content)
	lines := splitLines(content)
	totalLines := len(lines)

	if totalLines <= maxL && totalBytes <= maxB {
		return Result{
			Content:     content,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			MaxLines:    maxL,
			MaxBytes:    maxB,
		}
	}

	if len(lines) > 0 && len(lines[0]) > maxB {
		return Result{
			Truncated:             true,
			TruncatedBy:           ByBytes,
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			FirstLineExceedsLimit: true,
			MaxLines:              maxL,
			MaxBytes:              maxB,
		}
	}

	var out []string
	outBytes := 0
	truncatedBy := ByLines

	for i := 0; i < len(lines) && i < maxL; i++ {
		lineBytes := len(lines[i])
		if i > 0 {
			lineBytes++ // newline separator
		}
		if outBytes+lineBytes > maxB {
			truncatedBy = ByBytes
			break
		}
		out = append(out, lines[i])
		outBytes += lineBytes
	}

	if len(out) >= maxL && outBytes <= maxB {
		truncatedBy = ByLines
	}

	outputContent := strings.Join(out, "\n")
	return Result{
		Content:     outputContent,
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: len(out),
		OutputBytes: len(outputContent),
		MaxLines:    maxL,
		MaxBytes:    maxB,
	}
}

// Tail keeps the last N lines / N bytes. May return a partial first line
// when the last line alone exceeds MaxBytes.
func Tail(content string, opts Options) Result {
	maxL := opts.maxLines()
	maxB := opts.maxBytes()
	totalBytes := len(content)
	lines := splitLines(content)
	totalLines := len(lines)

	if totalLines <= maxL && totalBytes <= maxB {
		return Result{
			Content:     content,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			MaxLines:    maxL,
			MaxBytes:    maxB,
		}
	}

	var out []string
	outBytes := 0
	truncatedBy := ByLines

	for i := len(lines) - 1; i >= 0 && len(out) < maxL; i-- {
		lineBytes := len(lines[i])
		if len(out) > 0 {
			lineBytes++ // newline separator
		}
		if outBytes+lineBytes > maxB {
			truncatedBy = ByBytes
			if len(out) == 0 {
				partial := truncBytesFromEnd(lines[i], maxB)
				out = append(out, partial)
				outBytes = len(partial)
			}
			break
		}
		out = append(out, lines[i])
		outBytes += lineBytes
	}

	// reverse
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}

	if len(out) >= maxL && outBytes <= maxB {
		truncatedBy = ByLines
	}

	outputContent := strings.Join(out, "\n")
	return Result{
		Content:     outputContent,
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: len(out),
		OutputBytes: len(outputContent),
		MaxLines:    maxL,
		MaxBytes:    maxB,
	}
}

// Line truncates a single line to maxChars runes, appending "... [truncated]".
func Line(line string, maxChars int) (string, bool) {
	if utf8.RuneCountInString(line) <= maxChars {
		return line, false
	}
	runes := []rune(line)
	return string(runes[:maxChars]) + "... [truncated]", true
}

// FormatSize returns a human-readable size string.
func FormatSize(bytes int) string {
	return util.FormatSize(bytes)
}

// safeUTF8Cut truncates s to at most maxBytes, backing up to a valid
// UTF-8 rune boundary.
func safeUTF8Cut(s string, maxBytes int) string {
	if maxBytes >= len(s) {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

// truncBytesFromEnd keeps the last maxBytes of s at a UTF-8 boundary.
func truncBytesFromEnd(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
