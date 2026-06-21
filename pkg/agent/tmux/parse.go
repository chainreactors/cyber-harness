package tmux

import (
	"fmt"
	"strings"
)

// SplitCommandLine splits a command string into tokens, handling quoting and
// escaping. Comment-only lines (# ...) and blank lines are stripped.
func SplitCommandLine(input string) ([]string, error) {
	lines := strings.Split(input, "\n")
	var kept []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		kept = append(kept, line)
	}
	input = strings.Join(kept, " ")

	var tokens []string
	var cur strings.Builder
	var quote rune
	escaped := false

	for _, r := range input {
		if escaped {
			switch r {
			case '\\', '\'', '"', ' ', '\t', '\n', '\r':
				cur.WriteRune(r)
			default:
				cur.WriteRune('\\')
				cur.WriteRune(r)
			}
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			cur.WriteRune(r)
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}

	if escaped {
		cur.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}

// firstCommandToken extracts the first non-whitespace token from input,
// handling quotes and escapes.
func firstCommandToken(input string) string {
	input = strings.TrimSpace(input)
	var sb strings.Builder
	var quote rune
	escaped := false
	for _, r := range input {
		if escaped {
			sb.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			sb.WriteRune(r)
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			break
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// stripShellSyntax validates tokens for in-process command execution.
// Silently strips harmless stderr/stdout duplication (2>&1 etc).
// Rejects pipes, command chaining, and file redirections with clear errors.
func stripShellSyntax(tokens []string) ([]string, error) {
	clean := make([]string, 0, len(tokens))
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t == "|" || t == "||" {
			return nil, fmt.Errorf("pseudo-commands run in-process and do not support shell pipes (got %q). To limit output, use the scanner's own flags or call a separate filter step in a follow-up bash command", t)
		}
		if t == "&&" || t == ";" {
			return nil, fmt.Errorf("pseudo-commands do not support shell command chaining (got %q). Issue each command in a separate bash tool call", t)
		}
		if isStderrDup(t) {
			continue
		}
		if isFileRedirection(t) {
			return nil, fmt.Errorf("pseudo-commands do not support file redirection (got %q). They run in-process and return their output as the tool result; capture it from the result text instead", t)
		}
		clean = append(clean, t)
	}
	return clean, nil
}

func isStderrDup(token string) bool {
	switch token {
	case "2>&1", "1>&2", ">&2", ">&1":
		return true
	}
	return false
}

func isFileRedirection(token string) bool {
	switch token {
	case ">", ">>", "<", "<<", "2>", "1>", "0<", "&>", "&>>":
		return true
	}
	for _, prefix := range []string{
		"&>", "2>", "1>", "0<", ">>", ">", "<<", "<",
	} {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}
