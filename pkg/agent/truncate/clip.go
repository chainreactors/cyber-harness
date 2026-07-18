package truncate

import (
	"strings"
	"unicode/utf8"
)

// Clip collapses whitespace (newlines → spaces, multiple spaces → one),
// then truncates to maxLen bytes at a UTF-8 boundary, appending "...".
func Clip(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return "..."
	}
	cut := safeUTF8Cut(s, maxLen-3)
	return cut + "..."
}

// ClipRunes truncates by rune count, appending "…" (unicode ellipsis).
func ClipRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	if maxRunes <= 1 {
		return "…"
	}
	n := 0
	bytePos := 0
	for bytePos < len(s) && n < maxRunes-1 {
		_, size := utf8.DecodeRuneInString(s[bytePos:])
		bytePos += size
		n++
	}
	return s[:bytePos] + "…"
}

// ClipLines keeps at most maxLines lines, each truncated to maxWidth runes.
// Returns the selected lines and the count of hidden lines.
func ClipLines(text string, maxLines, maxWidth int) ([]string, int) {
	all := strings.Split(text, "\n")
	total := len(all)
	if maxLines <= 0 {
		return nil, total
	}
	if maxWidth < 0 {
		maxWidth = 0
	}
	if total > maxLines {
		all = all[:maxLines]
	}
	out := make([]string, len(all))
	for i, line := range all {
		if utf8.RuneCountInString(line) > maxWidth {
			runes := []rune(line)
			out[i] = string(runes[:maxWidth]) + "…"
		} else {
			out[i] = line
		}
	}
	hidden := total - len(out)
	if hidden < 0 {
		hidden = 0
	}
	return out, hidden
}
