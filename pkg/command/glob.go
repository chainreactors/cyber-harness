package command

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/chainreactors/aiscan/pkg/provider"
)

const maxGlobResults = 500

type GlobTool struct {
	workDir string
}

func NewGlobTool(workDir string) *GlobTool {
	return &GlobTool{workDir: workDir}
}

func (t *GlobTool) Name() string { return "glob" }

func (t *GlobTool) Description() string {
	return "Find files matching a glob pattern. Returns a list of matching file paths."
}

func (t *GlobTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Type: "function",
		Function: provider.FunctionDefinition{
			Name:        "glob",
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Glob pattern to match files (e.g., '*.go', 'src/**/*.js')",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Base directory for the search (default: working directory)",
					},
				},
				"required": []string{"pattern"},
			},
		},
	}
}

func (t *GlobTool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	baseDir := t.workDir
	if args.Path != "" {
		if filepath.IsAbs(args.Path) {
			baseDir = args.Path
		} else {
			baseDir = filepath.Join(t.workDir, args.Path)
		}
	}

	pattern := filepath.Join(baseDir, args.Pattern)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("glob error: %w", err)
	}

	if len(matches) == 0 {
		return "no files matched", nil
	}

	if len(matches) > maxGlobResults {
		matches = matches[:maxGlobResults]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("found %d files:\n", len(matches)))
	for _, m := range matches {
		rel, err := filepath.Rel(t.workDir, m)
		if err != nil {
			rel = m
		}
		sb.WriteString(rel + "\n")
	}

	return sb.String(), nil
}
