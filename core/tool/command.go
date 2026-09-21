package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	coreregistry "github.com/chainreactors/cyber/core/registry"
)

var (
	ErrInvalidCommand   = errors.New("invalid command registration")
	ErrDuplicateCommand = coreregistry.ErrDuplicate
	ErrUnavailable      = coreregistry.ErrUnavailable
)

// Command is an immutable native command declaration. Its dependencies are
// captured by Run when the owning extension is constructed.
type Command struct {
	Name  string
	Usage string
	// QuickReference is the command's inline reference in the system prompt. A
	// command that declares none is listed by its Usage summary line, so a Usage
	// that opens with the generated "Usage:" header has to declare one.
	QuickReference  string
	DescriptionPath string
	Run             func(context.Context, *Execution) (any, error)
}

// StripShellSyntax rejects shell constructs a pseudo-command cannot honor.
func StripShellSyntax(tokens []string) ([]string, error) {
	clean := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token == "|" || token == "||" {
			return nil, fmt.Errorf("pseudo-commands run in-process and do not support shell pipes (got %q). To limit output, use the command's own flags or call a separate filter step", token)
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
