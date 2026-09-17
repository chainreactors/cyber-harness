package commands

import (
	"strings"

	"github.com/chainreactors/aiscan/pkg/types"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func parseShellCommand(command string) (*syntax.File, error) {
	return syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
}

// literalBuiltin proves that bypassing the shell preserves the command's
// meaning. Never infer shell syntax from argv: quoting has already been lost.
func (t *BashTool) literalBuiltin(script *syntax.File) (*types.CommandSpec, []string, bool) {
	if len(script.Stmts) != 1 || !plainStatement(script.Stmts[0]) {
		return nil, nil, false
	}
	call, ok := script.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) != 0 || len(call.Args) == 0 {
		return nil, nil, false
	}
	for _, word := range call.Args {
		if !literalParts(word.Parts, false) {
			return nil, nil, false
		}
	}
	// Use a fresh configuration for concurrent calls. The AST check excludes
	// environment, filesystem and command expansion; Fields only removes quotes
	// and escapes, including quoted empty arguments.
	argv, err := expand.Fields(&expand.Config{}, call.Args...)
	if err != nil || len(argv) == 0 {
		return nil, nil, false
	}
	command, ok := t.resolve(argv[0])
	if !ok {
		return nil, nil, false
	}
	return command, normalizeNoColor(command.Name, argv[1:]), true
}

func plainStatement(stmt *syntax.Stmt) bool {
	return !stmt.Negated && !stmt.Background && !stmt.Coprocess && !stmt.Disown && len(stmt.Redirs) == 0
}

func literalParts(parts []syntax.WordPart, quoted bool) bool {
	for _, part := range parts {
		switch part := part.(type) {
		case *syntax.Lit:
			if !quoted {
				for i := 0; i < len(part.Value); i++ {
					if part.Value[i] == '\\' {
						i++
					} else if strings.ContainsRune("*?[{~", rune(part.Value[i])) {
						return false
					}
				}
			}
		case *syntax.SglQuoted:
			if part.Dollar {
				return false
			}
		case *syntax.DblQuoted:
			if part.Dollar || !literalParts(part.Parts, true) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// splitPipeline retains the legacy native-to-shell bridge only for an actual
// foreground pipeline. Operator positions refer to the original script, so
// quotes and nested shell constructs cannot be mistaken for the separator.
func splitPipeline(script *syntax.File, command string) (left, right string, ok bool) {
	if len(script.Stmts) != 1 || !plainStatement(script.Stmts[0]) {
		return "", "", false
	}
	pipe, ok := script.Stmts[0].Cmd.(*syntax.BinaryCmd)
	if !ok || pipe.Op != syntax.Pipe {
		return "", "", false
	}
	// A heredoc body can follow the pipe on later lines. Only the full shell
	// adapter may execute that script; slicing at the pipe would move its body.
	hasHeredoc := false
	syntax.Walk(script, func(node syntax.Node) bool {
		if redir, ok := node.(*syntax.Redirect); ok && redir.Hdoc != nil {
			hasHeredoc = true
		}
		return !hasHeredoc
	})
	if hasHeredoc {
		return "", "", false
	}
	for plainStatement(pipe.X) {
		nested, ok := pipe.X.Cmd.(*syntax.BinaryCmd)
		if !ok || nested.Op != syntax.Pipe {
			break
		}
		pipe = nested
	}
	offset := int(pipe.OpPos.Offset())
	return command[:offset], command[offset+1:], true
}

func (t *BashTool) hasRegisteredCommand(script *syntax.File) bool {
	found := false
	syntax.Walk(script, func(node syntax.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if ok && len(call.Args) > 0 && literalParts(call.Args[0].Parts, false) {
			argv, err := expand.Fields(&expand.Config{}, call.Args[0])
			if err == nil && len(argv) == 1 {
				_, found = t.resolve(argv[0])
			}
		}
		return !found
	})
	return found
}
