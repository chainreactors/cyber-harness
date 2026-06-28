package output

import (
	"regexp"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent/truncate"
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

func AssetItemDetail(item AssetItem) string {
	for _, value := range []string{item.Detail, item.Raw} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			if value == item.Raw {
				if parsed := ExtractQuotedMarkdown(value); parsed != "" {
					return parsed
				}
			}
			return trimmed
		}
	}
	return ""
}

func ExtractQuotedMarkdown(raw string) string {
	fields := quotedFields(raw)
	for i := len(fields) - 1; i >= 0; i-- {
		value := strings.TrimSpace(fields[i])
		if value == "" {
			continue
		}
		if looksLikeMarkdown(value) {
			return value
		}
	}
	return ""
}

func ExtractQuotedSummary(raw string) string {
	fields := quotedFields(raw)
	if len(fields) == 0 {
		return ""
	}
	for _, value := range fields {
		value = strings.TrimSpace(value)
		if value == "" || looksLikeMarkdown(value) {
			continue
		}
		return value
	}
	return ""
}

func quotedFields(input string) []string {
	var values []string
	for i := 0; i < len(input); i++ {
		if input[i] != '"' {
			continue
		}
		i++
		var sb strings.Builder
		for i < len(input) {
			ch := input[i]
			if ch == '"' {
				break
			}
			if ch == '\\' && i+1 < len(input) {
				sb.WriteString(decodeEscapedByte(input[i+1]))
				i += 2
				continue
			}
			sb.WriteByte(ch)
			i++
		}
		values = append(values, sb.String())
	}
	return values
}

func decodeEscapedByte(ch byte) string {
	switch ch {
	case 'n':
		return "\n"
	case 'r':
		return "\r"
	case 't':
		return "\t"
	case '"':
		return `"`
	case '\\':
		return `\`
	default:
		return string(ch)
	}
}

func looksLikeMarkdown(value string) bool {
	if strings.Contains(value, "\n") {
		return true
	}
	for _, prefix := range []string{"#", "-", "*", "|", ">", "```"} {
		if strings.HasPrefix(strings.TrimSpace(value), prefix) {
			return true
		}
	}
	return false
}

func firstContentLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
