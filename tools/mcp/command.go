package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	coretool "github.com/chainreactors/cyber/core/tool"
)

// Command exposes tools as subcommands of the configured server alias.
// Discovery and argument conversion happen at runtime, without code generation
// or expanding the model's function-tool list for large MCP catalogs.
func (c *Catalog) Command() coretool.Command {
	name := c.connection.name
	usage := fmt.Sprintf(`%s — %d MCP tools

Usage:
  %s --list                           list tool names and descriptions (JSON)
  %s <tool> --help                    show parameters and the complete declaration
  %s <tool> --<parameter> <value> ...  call with schema-derived parameter flags
  %s <tool> --json '<object>'         call with a complete JSON argument object
  %s <tool> --file <path>             read arguments from a JSON file

Results are complete MCP JSON on stdout. Tool isError and protocol failures
fail the command. Tool and parameter names keep their upstream spelling.
Relative argument files resolve against the caller's working directory.
Use %s -- <tool> to address a tool named --help, -h or --list.
`, name, len(c.tools), name, name, name, name, name, name)
	return coretool.Command{
		Name: name, Usage: usage,
		QuickReference: fmt.Sprintf("- %s: %d MCP tools; discover with `%s --list`, inspect `%s <tool> --help`, invoke `%s <tool> --<parameter> <value>`.", name, len(c.tools), name, name, name),
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
	if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := io.WriteString(output, usage)
		return err
	}
	if args[0] == "--list" {
		if len(args) != 1 {
			return fmt.Errorf("usage: %s --list", c.connection.name)
		}
		// Progressive discovery: schemas stay behind per-tool help.
		type summary struct {
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
		}
		tools := make([]summary, 0, len(c.tools))
		for _, d := range c.tools {
			tools = append(tools, summary{d.Name, d.Description})
		}
		return json.NewEncoder(output).Encode(tools)
	}
	if args[0] == "--" {
		args = args[1:]
		if len(args) == 0 {
			return fmt.Errorf("expected a tool name after --")
		}
	}
	d, err := c.lookup(args[0])
	if err != nil {
		return err
	}
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		return c.toolHelp(output, d)
	}
	var arguments json.RawMessage
	if len(args) > 1 && (args[1] == "--json" || args[1] == "--file") {
		arguments, err = rawArguments(ctx, args[1:], execution.Dir)
	} else {
		arguments, err = namedArguments(d.InputSchema, args[1:])
	}
	if err != nil {
		return err
	}
	raw, callErr := c.Call(ctx, d.Name, arguments)
	if len(raw) != 0 {
		callErr = errors.Join(callErr, writeJSON(output, raw))
	}
	return callErr
}

func (c *Catalog) toolHelp(output io.Writer, d declaration) error {
	var text strings.Builder
	fmt.Fprintf(&text, "%s\n\nUsage: %s %s [--<parameter> <value> ...]\n\nParameters:\n", d.Description, c.connection.name, d.Name)
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if json.Unmarshal(d.InputSchema, &schema) == nil {
		names := make([]string, 0, len(schema.Properties))
		for name := range schema.Properties {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			raw := schema.Properties[name]
			var parameter struct {
				Description string `json:"description"`
			}
			_ = json.Unmarshal(raw, &parameter)
			flag := "--" + name
			if !parameterFlag(name) {
				flag = fmt.Sprintf("%q (use --json/--file)", name)
			}
			required := ""
			if slices.Contains(schema.Required, name) {
				required = " (required)"
			}
			fmt.Fprintf(&text, "  %s <%s>%s  %s\n", flag, parameterType(raw), required, strings.Join(strings.Fields(parameter.Description), " "))
		}
	}
	text.WriteString("\nEvery parameter flag takes one value; --name=value is also accepted.\nObjects, arrays and ambiguous types take JSON. Optional defaults stay omitted.\nUse --json '<object>' or --file <path> for a complete argument object,\nincluding nulls, unusual property names and parameters named help, json or file.\nThe upstream server validates the complete schema.\n\nUpstream declaration:\n")
	if _, err := io.WriteString(output, text.String()); err != nil {
		return err
	}
	return writeJSON(output, d.raw)
}

func rawArguments(ctx context.Context, args []string, directory string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) != 2 {
		return nil, fmt.Errorf("expected exactly one --json '<object>' or --file <path>; cannot mix raw input with parameter flags")
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
