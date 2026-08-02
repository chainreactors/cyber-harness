package output

import (
	"regexp"
	"strings"

	"github.com/chainreactors/aiscan/core/truncate"
)

var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\)|[PX^_].*?\x1b\\|[@-_])`)

func StripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func OutputPrefix(source string, colorFn func(string) string) string {
	return colorFn("[" + source + "]")
}

func FormatLine(prefix, body string, color Color) string {
	body = strings.TrimSpace(body)
	parts := []string{prefix}
	if body != "" {
		parts = append(parts, body)
	}
	return SanitizeLine(strings.Join(parts, " "), color)
}

func SanitizeLine(line string, color Color) string {
	line = strings.TrimSpace(line)
	if !color.Enabled {
		line = StripANSI(line)
	}
	return line
}

func TruncateStr(s string, maxLen int) string {
	return truncate.Clip(s, maxLen)
}

func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func CompactStrings(values ...string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
