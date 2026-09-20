// SplitCommandLine parses command text without executing it.
package tool

import (
	"fmt"
	"strings"
)

func SplitCommandLine(input string) ([]string, error) {
	lines := strings.Split(input, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			kept = append(kept, line)
		}
	}
	input = strings.Join(kept, " ")

	var tokens []string
	var current strings.Builder
	var quote rune
	escaped := false
	for _, value := range input {
		if escaped {
			switch value {
			case '\\', '\'', '"', ' ', '\t', '\n', '\r':
				current.WriteRune(value)
			default:
				current.WriteRune('\\')
				current.WriteRune(value)
			}
			escaped = false
			continue
		}
		if value == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if value == quote {
				quote = 0
			} else {
				current.WriteRune(value)
			}
			continue
		}
		if value == '\'' || value == '"' {
			quote = value
			continue
		}
		if strings.ContainsRune(" \t\n\r", value) {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(value)
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens, nil
}

func JoinCommandLine(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteCommandArg(name))
	for _, arg := range args {
		parts = append(parts, quoteCommandArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteCommandArg(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\r\n\"'\\|;&<>") {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}
