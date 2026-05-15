package ioa

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/pkg/command"
	ioaclient "github.com/chainreactors/ioa/client"
)

func NewCommands(client ioaclient.API, nodeName string, meta map[string]any) []command.PseudoCommand {
	var cmds []command.PseudoCommand
	for _, t := range ioaclient.NewTools(client, ioaclient.ToolOptions{NodeName: nodeName, NodeMeta: meta}) {
		cmds = append(cmds, &toolAdapter{tool: t})
	}
	return cmds
}

type toolAdapter struct {
	tool ioaclient.Tool
}

func (a *toolAdapter) Name() string { return a.tool.Name() }

func (a *toolAdapter) Usage() string {
	def := a.tool.Definition()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s - %s\n", def.Function.Name, a.tool.Description()))

	params := def.Function.Parameters
	props, _ := params["properties"].(map[string]interface{})
	reqRaw, _ := params["required"].([]interface{})
	required := make([]string, 0, len(reqRaw))
	for _, r := range reqRaw {
		if s, ok := r.(string); ok {
			required = append(required, s)
		}
	}
	requiredSet := make(map[string]bool, len(required))
	for _, r := range required {
		requiredSet[r] = true
	}

	if len(props) > 0 {
		sb.WriteString("\nUsage:\n")
		sb.WriteString(fmt.Sprintf("  %s", def.Function.Name))
		for name := range props {
			if requiredSet[name] {
				sb.WriteString(fmt.Sprintf(" --%s <value>", name))
			} else {
				sb.WriteString(fmt.Sprintf(" [--%s <value>]", name))
			}
		}
		sb.WriteString("\n\nOptions:\n")
		for name, schema := range props {
			desc := ""
			if m, ok := schema.(map[string]interface{}); ok {
				desc, _ = m["description"].(string)
			}
			marker := ""
			if requiredSet[name] {
				marker = " (required)"
			}
			sb.WriteString(fmt.Sprintf("  --%-16s %s%s\n", name, desc, marker))
		}
	}
	return sb.String()
}

func (a *toolAdapter) Execute(ctx context.Context, args []string) (string, error) {
	jsonArgs, err := argsToJSON(args)
	if err != nil {
		return "", fmt.Errorf("%s: %w\n\n%s", a.tool.Name(), err, a.Usage())
	}
	return a.tool.Execute(ctx, jsonArgs)
}

func argsToJSON(args []string) (string, error) {
	m := make(map[string]interface{})
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		key := strings.TrimPrefix(arg, "--")
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			m[key] = true
			continue
		}
		i++
		val := args[i]
		if val == "true" {
			m[key] = true
		} else if val == "false" {
			m[key] = false
		} else if n, err := parseInt(val); err == nil {
			m[key] = n
		} else if json.Valid([]byte(val)) && (val[0] == '{' || val[0] == '[') {
			var v interface{}
			if err := json.Unmarshal([]byte(val), &v); err != nil {
				return "", fmt.Errorf("parse %s JSON: %w", key, err)
			}
			m[key] = v
		} else {
			m[key] = val
		}
	}
	data, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseInt(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}
