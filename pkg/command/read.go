package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainreactors/aiscan/pkg/provider"
)

const maxReadSize = 100 * 1024 // 100KB

type ReadTool struct {
	workDir string
	readers []VirtualFileReader
}

type VirtualFileReader interface {
	ReadVirtual(path string) (content string, handled bool, err error)
}

func NewReadTool(workDir string, readers ...VirtualFileReader) *ReadTool {
	return &ReadTool{workDir: workDir, readers: readers}
}

func (t *ReadTool) Name() string { return "read" }

func (t *ReadTool) Description() string {
	return "Read the contents of a file. Returns the file content as text. Use offset and limit for large files."
}

func (t *ReadTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Type: "function",
		Function: provider.FunctionDefinition{
			Name:        "read",
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path to read (absolute or relative to working directory)",
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": "Line number to start reading from (0-based)",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of lines to read",
					},
				},
				"required": []string{"path"},
			},
		},
	}
}

func (t *ReadTool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	content, err := t.read(args.Path)
	if err != nil {
		return "", err
	}
	if len(content) > maxReadSize {
		content = content[:maxReadSize] + "\n... (file truncated)"
	}

	if args.Offset > 0 || args.Limit > 0 {
		lines := strings.Split(content, "\n")
		start := args.Offset
		if start >= len(lines) {
			return "", fmt.Errorf("offset %d exceeds file line count %d", start, len(lines))
		}
		end := len(lines)
		if args.Limit > 0 && start+args.Limit < end {
			end = start + args.Limit
		}
		content = strings.Join(lines[start:end], "\n")
	}

	return content, nil
}

func (t *ReadTool) read(path string) (string, error) {
	for _, reader := range t.readers {
		if reader == nil {
			continue
		}
		content, handled, err := reader.ReadVirtual(path)
		if !handled {
			continue
		}
		if err != nil {
			return "", err
		}
		return content, nil
	}

	resolved := t.resolvePath(path)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return string(data), nil
}

func (t *ReadTool) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(t.workDir, path)
}
