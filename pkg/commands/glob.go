package commands

import (
	"context"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent/truncate"
)

const maxGlobResults = truncate.MaxGlobResults

type VirtualGlobber interface {
	GlobVirtual(pattern string) ([]string, bool)
}

type GlobTool struct {
	workDir  string
	globbers []VirtualGlobber
}

func NewGlobTool(workDir string, globbers ...VirtualGlobber) *GlobTool {
	return &GlobTool{workDir: workDir, globbers: globbers}
}

func (t *GlobTool) Name() string { return "glob" }

func (t *GlobTool) Description() string {
	return "Find files matching a glob pattern. Supports ** for recursive directory matching. Returns a list of matching file paths relative to the working directory."
}

type GlobArgs struct {
	Pattern string `json:"pattern"        jsonschema:"description=Glob pattern to match files. Supports * and ** for recursive matching (e.g. *.go or src/**/*.js)"`
	Path    string `json:"path,omitempty" jsonschema:"description=Base directory for the search (default: working directory)"`
}

func (t *GlobTool) Definition() ToolDefinition {
	return ToolDef("glob", t.Description(), GlobArgs{})
}


func (t *GlobTool) Execute(ctx context.Context, arguments string) (ToolResult, error) {
	args, err := ParseArgs[GlobArgs](arguments)
	if err != nil {
		return ToolResult{}, err
	}

	if args.Pattern == "" {
		return ToolResult{}, fmt.Errorf("pattern is required")
	}

	baseDir := t.workDir
	if args.Path != "" {
		if filepath.IsAbs(args.Path) {
			baseDir = args.Path
		} else {
			baseDir = filepath.Join(t.workDir, args.Path)
		}
	}

	var matches []string

	if strings.Contains(args.Pattern, "**") {
		matches, err = globRecursive(baseDir, args.Pattern)
	} else {
		pattern := filepath.Join(baseDir, args.Pattern)
		matches, err = filepath.Glob(pattern)
	}
	if err != nil {
		return ToolResult{}, fmt.Errorf("glob error: %w", err)
	}

	// Also search virtual/embedded files
	searchPattern := args.Pattern
	if args.Path != "" {
		searchPattern = filepath.Join(args.Path, args.Pattern)
	}
	for _, g := range t.globbers {
		if g == nil {
			continue
		}
		if virtualMatches, ok := g.GlobVirtual(searchPattern); ok {
			matches = mergeUnique(matches, virtualMatches)
		}
	}

	if len(matches) == 0 {
		return TextResult("no files matched"), nil
	}

	truncated := false
	if len(matches) > maxGlobResults {
		matches = matches[:maxGlobResults]
		truncated = true
	}

	var sb strings.Builder
	for _, m := range matches {
		rel, err := filepath.Rel(t.workDir, m)
		if err != nil {
			rel = m
		}
		sb.WriteString(rel + "\n")
	}

	summary := fmt.Sprintf("found %d files", len(matches))
	if truncated {
		summary += fmt.Sprintf(" (showing first %d, narrow your pattern)", maxGlobResults)
	}
	sb.WriteString(summary)

	return TextResult(sb.String()), nil
}

// globRecursive handles patterns containing ** by walking the directory tree.
// The pattern is split on "**" — the prefix selects subdirectories to walk,
// and the suffix is matched against each file within.
func globRecursive(baseDir, pattern string) ([]string, error) {
	pattern = filepath.FromSlash(pattern)
	parts := strings.SplitN(pattern, "**", 2)
	prefix := strings.TrimRight(parts[0], `/\`)
	suffix := ""
	if len(parts) > 1 {
		suffix = strings.TrimLeft(parts[1], `/\`)
	}

	root := baseDir
	if prefix != "" {
		root = filepath.Join(baseDir, prefix)
	}

	if _, err := os.Stat(root); err != nil {
		return nil, err
	}

	var matches []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		if len(matches) >= maxGlobResults*2 {
			return filepath.SkipAll
		}

		if suffix == "" {
			matches = append(matches, path)
			return nil
		}

		// Match the suffix against the relative path from root
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		matched := matchRecursiveSuffix(rel, suffix)
		if matched {
			matches = append(matches, path)
		}
		return nil
	})

	return matches, nil
}

func matchRecursiveSuffix(rel, suffix string) bool {
	rel = filepath.ToSlash(rel)
	suffix = filepath.ToSlash(suffix)
	if !strings.Contains(suffix, "/") {
		matched, _ := pathpkg.Match(suffix, pathpkg.Base(rel))
		return matched
	}

	if matched, _ := pathpkg.Match(suffix, rel); matched {
		return true
	}
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		candidate := strings.Join(parts[i:], "/")
		if matched, _ := pathpkg.Match(suffix, candidate); matched {
			return true
		}
	}
	return false
}

func mergeUnique(a, b []string) []string {
	seen := make(map[string]struct{}, len(a))
	for _, s := range a {
		seen[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			a = append(a, s)
		}
	}
	return a
}
