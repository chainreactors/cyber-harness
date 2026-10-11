package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	coretool "github.com/chainreactors/cyber/core/tool"
)

// Command automatically exposes this server's catalog as one native CLI.
// Keeping upstream names as argv values avoids renaming/collision/loss and
// does not inflate the model's function-tool list for large MCP catalogs.
func (c *Catalog) Command() coretool.Command {
	name := "mcp-" + c.connection.name
	usage := fmt.Sprintf(`%s — %d MCP tools

Usage:
  %s tools                             list tool names and descriptions (JSON)
  %s schema <tool>                     show the complete upstream declaration
  %s call <tool> [--json '<object>']    call a tool; defaults to {}
  %s call <tool> --file <path>          read arguments from a JSON file
  %s --help                           show this help

Results are complete MCP JSON on stdout. Tool isError and protocol failures
fail the command. Use single quotes around inline JSON in bash. Relative
argument files resolve against the caller's working directory.
`, name, len(c.tools), name, name, name, name, name)
	return coretool.Command{
		Name: name, Usage: usage,
		QuickReference: fmt.Sprintf("- %s: %d MCP tools; discover with `%s tools`, inspect `%s schema <tool>`, invoke `%s call <tool> --json '<object>'`.", name, len(c.tools), name, name, name),
		Run: func(ctx context.Context, execution *coretool.Execution) (any, error) {
			return nil, c.run(ctx, execution, usage)
		},
	}
}

func (c *Catalog) run(ctx context.Context, execution *coretool.Execution, usage string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	args := execution.Args
	output := execution.Stdout
	if output == nil {
		output = io.Discard
	}
	if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "help" || args[0] == "-h") {
		_, err := io.WriteString(output, usage)
		return err
	}
	switch args[0] {
	case "tools":
		if len(args) != 1 {
			return fmt.Errorf("usage: mcp-%s tools", c.connection.name)
		}
		// Progressive discovery: schemas stay behind the schema subcommand.
		type summary struct {
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
		}
		tools := make([]summary, 0, len(c.tools))
		for _, d := range c.tools {
			tools = append(tools, summary{d.Name, d.Description})
		}
		return json.NewEncoder(output).Encode(tools)
	case "schema":
		if len(args) != 2 {
			return fmt.Errorf("usage: mcp-%s schema <tool>", c.connection.name)
		}
		d, err := c.lookup(args[1])
		if err != nil {
			return err
		}
		return writeJSON(output, d.raw)
	case "call":
		if len(args) < 2 {
			return fmt.Errorf("usage: mcp-%s call <tool> [--json '<object>' | --file <path>]", c.connection.name)
		}
		if _, err := c.lookup(args[1]); err != nil {
			return err
		}
		if len(args) == 3 && args[2] == "--help" {
			d, _ := c.lookup(args[1])
			return writeJSON(output, d.raw)
		}
		arguments, err := callArguments(ctx, args[2:], execution.Dir)
		if err != nil {
			return err
		}
		raw, callErr := c.Call(ctx, args[1], arguments)
		if len(raw) != 0 {
			callErr = errors.Join(callErr, writeJSON(output, raw))
		}
		return callErr
	default:
		return fmt.Errorf("unknown MCP subcommand %q; run mcp-%s --help", args[0], c.connection.name)
	}
}

func callArguments(ctx context.Context, args []string, directory string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if len(args) != 2 || (args[0] != "--json" && args[0] != "--file") {
		return nil, fmt.Errorf("expected exactly one --json '<object>' or --file <path>; quote JSON as one shell argument")
	}
	if args[0] == "--json" {
		return json.RawMessage(args[1]), nil
	}
	path := args[1]
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("--file requires a path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(directory, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// The adapter owns this file, so cancellation can interrupt its read.
	stop := context.AfterFunc(ctx, func() { _ = file.Close() })
	defer stop()
	data, err := io.ReadAll(file)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return data, err
}

func writeJSON(output io.Writer, raw json.RawMessage) error {
	if _, err := output.Write(raw); err != nil {
		return err
	}
	_, err := io.WriteString(output, "\n")
	return err
}
