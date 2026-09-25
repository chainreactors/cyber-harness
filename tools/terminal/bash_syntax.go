package terminal

import (
	"strings"

	"github.com/chainreactors/cyber/core/types"
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
	// A fresh configuration keeps concurrent calls independent. The AST check
	// excludes environment, filesystem and command expansion; Fields only
	// removes quotes and escapes, including quoted empty arguments.
	argv, err := expand.Fields(&expand.Config{}, call.Args...)
	if err != nil || len(argv) == 0 {
		return nil, nil, false
	}
	command, ok := t.resolve(argv[0])
	if !ok {
		return nil, nil, false
	}
	return command, append([]string(nil), argv[1:]...), true
}

func (t *BashTool) literalExternal(script *syntax.File) ([]string, bool) {
	if len(script.Stmts) != 1 || !plainStatement(script.Stmts[0]) {
		return nil, false
	}
	call, ok := script.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) != 0 || len(call.Args) == 0 {
		return nil, false
	}
	for _, word := range call.Args {
		if !literalParts(word.Parts, false) {
			return nil, false
		}
	}
	argv, err := expand.Fields(&expand.Config{}, call.Args...)
	if err != nil || len(argv) == 0 || argv[0] == "" {
		return nil, false
	}
	if _, registered := t.resolve(argv[0]); registered {
		return nil, false
	}
	return argv, true
}

func plainStatement(stmt *syntax.Stmt) bool {
	return stmt != nil && !stmt.Negated && !stmt.Background && !stmt.Coprocess && !stmt.Disown && len(stmt.Redirs) == 0
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
