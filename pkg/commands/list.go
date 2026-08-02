package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	coretool "github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/core/truncate"
)

// ListTool lists directory entries through the host filesystem API.
type ListTool struct {
	workDir string
}

func NewListTool(workDir string) *ListTool {
	return &ListTool{workDir: workDir}
}

func (t *ListTool) Name() string { return "ls" }

func (t *ListTool) Description() string {
	return "List a directory using native filesystem access. Use this instead of bash ls. Returns structured JSON with names, types, and sizes."
}

type ListArgs struct {
	Path string `json:"path,omitempty" jsonschema:"description=Directory path to list (absolute or relative to working directory; default: .)"`
}

type ListEntry struct {
	Name        string `json:"name"`
	IsDirectory bool   `json:"isDirectory"`
	Size        int64  `json:"size"`
}

type ListResult struct {
	Path      string      `json:"path"`
	Entries   []ListEntry `json:"entries"`
	Truncated bool        `json:"truncated,omitempty"`
}

func (t *ListTool) Definition() *coretool.Definition {
	return coretool.Def("ls", t.Description(), ListArgs{})
}

func (t *ListTool) Execute(ctx context.Context, arguments string) (*coretool.Result, error) {
	args, err := coretool.ParseArgs[ListArgs](arguments)
	if err != nil {
		return nil, err
	}
	if args.Path == "" {
		args.Path = "."
	}

	workDir := coretool.WorkDirFromContext(ctx, t.workDir)
	resolved := args.Path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(workDir, resolved)
	}
	entries, err := os.ReadDir(filepath.Clean(resolved))
	if err != nil {
		return nil, fmt.Errorf("list directory: %w", err)
	}

	result := ListResult{Path: args.Path, Entries: make([]ListEntry, 0, min(len(entries), truncate.MaxGlobResults))}
	if len(entries) > truncate.MaxGlobResults {
		entries = entries[:truncate.MaxGlobResults]
		result.Truncated = true
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", entry.Name(), err)
		}
		result.Entries = append(result.Entries, ListEntry{
			Name:        entry.Name(),
			IsDirectory: entry.IsDir(),
			Size:        info.Size(),
		})
	}

	content, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, err
	}
	return coretool.TextResult(string(content)), nil
}
