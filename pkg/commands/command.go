package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	coreregistry "github.com/chainreactors/aiscan/core/registry"
)

var (
	ErrInvalidCommand   = errors.New("invalid command registration")
	ErrDuplicateCommand = coreregistry.ErrDuplicate
	ErrUnavailable      = coreregistry.ErrUnavailable
)

// Command is an immutable native command declaration. Its dependencies are
// captured by Run when the owning extension is constructed.
type Command struct {
	Name            string
	Usage           string
	QuickReference  string
	DescriptionPath string
	Run             func(context.Context, *Execution) (any, error)
}

func stripShellSyntax(tokens []string) ([]string, error) {
	clean := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token == "|" || token == "||" {
			return nil, fmt.Errorf("pseudo-commands run in-process and do not support shell pipes (got %q). To limit output, use the scanner's own flags or call a separate filter step", token)
		}
		if token == "&&" || token == ";" {
			return nil, fmt.Errorf("pseudo-commands do not support shell command chaining (got %q). Issue each command separately", token)
		}
		if isStderrDup(token) {
			continue
		}
		if isFileRedirection(token) {
			return nil, fmt.Errorf("pseudo-commands do not support file redirection (got %q); use the returned tool result", token)
		}
		clean = append(clean, token)
	}
	return clean, nil
}

func isStderrDup(token string) bool {
	switch token {
	case "2>&1", "1>&2", ">&2", ">&1":
		return true
	default:
		return false
	}
}

func isFileRedirection(token string) bool {
	switch token {
	case ">", ">>", "<", "<<", "2>", "1>", "0<", "&>", "&>>":
		return true
	}
	for _, prefix := range []string{"&>", "2>", "1>", "0<", ">>", ">", "<<", "<"} {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}

func normalizeNoColor(name string, args []string) []string {
	if name != "scan" {
		return args
	}
	for _, arg := range args {
		if arg == "--no-color" {
			return args
		}
	}
	return append(args, "--no-color")
}

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
	if arg != "" && !strings.ContainsAny(arg, " \\t\\r\\n\\\"'\\\\|;&<>") {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\\\''") + "'"
}
