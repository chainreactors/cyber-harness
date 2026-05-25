package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
)

type WriteTool struct {
	workDir string
}

func NewWriteTool(workDir string) *WriteTool {
	return &WriteTool{workDir: workDir}
}

func (t *WriteTool) Name() string { return "write" }

func (t *WriteTool) Description() string {
	return "Write or edit a file. Two modes:\n" +
		"(1) Write — provide 'content' to create or overwrite a file.\n" +
		"(2) Edit — provide 'edits' array with targeted replacements. " +
		"Each edit's old_text must match exactly in the original file. " +
		"All edits are matched against the original content, not incrementally. " +
		"Do not include overlapping edits; merge them into one instead."
}

type EditPatch struct {
	OldText    string `json:"old_text"               jsonschema:"description=Exact text to find and replace. Must be unique in the file unless replace_all is true."`
	NewText    string `json:"new_text"                jsonschema:"description=Replacement text for this edit."`
	ReplaceAll bool   `json:"replace_all,omitempty"   jsonschema:"description=Replace all occurrences of old_text instead of requiring uniqueness."`
}

type WriteArgs struct {
	Path    string      `json:"path"             jsonschema:"description=File path to write or edit (absolute or relative to working directory)"`
	Content string      `json:"content,omitempty" jsonschema:"description=Full file content for write mode. Ignored when edits is provided."`
	Edits   []EditPatch `json:"edits,omitempty"   jsonschema:"description=One or more targeted replacements. Each edit is matched against the original file. Do not include overlapping edits."`
}

func (t *WriteTool) Definition() provider.ToolDefinition {
	return ToolDef("write", t.Description(), WriteArgs{})
}

func (t *WriteTool) Execute(ctx context.Context, arguments string) (ToolResult, error) {
	args, err := ParseArgs[WriteArgs](arguments)
	if err != nil {
		return ToolResult{}, err
	}

	if args.Path == "" {
		return ToolResult{}, fmt.Errorf("path is required")
	}

	if len(args.Edits) > 0 {
		return t.editFile(args)
	}

	return t.writeFile(args)
}

func (t *WriteTool) writeFile(args WriteArgs) (ToolResult, error) {
	path := t.resolvePath(args.Path)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ToolResult{}, fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(args.Content), 0644); err != nil {
		return ToolResult{}, fmt.Errorf("write file: %w", err)
	}

	lineCount := strings.Count(args.Content, "\n") + 1
	return TextResult(fmt.Sprintf("wrote %d bytes (%d lines) to %s", len(args.Content), lineCount, args.Path)), nil
}

type editMatch struct {
	editIndex  int
	matchIndex int
	matchLen   int
	newText    string
}

func (t *WriteTool) editFile(args WriteArgs) (ToolResult, error) {
	path := t.resolvePath(args.Path)

	data, err := os.ReadFile(path)
	if err != nil {
		return ToolResult{}, fmt.Errorf("read file for edit: %w", err)
	}
	original := string(data)

	// Phase 1: validate all edits against the original content
	var matches []editMatch
	for i, edit := range args.Edits {
		if edit.OldText == "" {
			return ErrorResult(fmt.Sprintf("edits[%d]: old_text must not be empty", i)), nil
		}
		if edit.OldText == edit.NewText {
			return ErrorResult(fmt.Sprintf("edits[%d]: old_text and new_text are identical", i)), nil
		}

		count := strings.Count(original, edit.OldText)
		if count == 0 {
			hint := edit.OldText
			if len(hint) > 200 {
				hint = hint[:200] + "..."
			}
			return ErrorResult(fmt.Sprintf("edits[%d]: old_text not found in %s. Make sure it matches exactly (including whitespace and indentation).\nSearched for:\n%s", i, args.Path, hint)), nil
		}
		if count > 1 && !edit.ReplaceAll {
			return ErrorResult(fmt.Sprintf("edits[%d]: old_text matches %d locations in %s. Either set replace_all:true or include more surrounding context to disambiguate.", i, count, args.Path)), nil
		}

		if edit.ReplaceAll {
			// For replace_all, record the first match position for overlap detection
			idx := strings.Index(original, edit.OldText)
			matches = append(matches, editMatch{
				editIndex:  i,
				matchIndex: idx,
				matchLen:   len(edit.OldText),
				newText:    edit.NewText,
			})
		} else {
			idx := strings.Index(original, edit.OldText)
			matches = append(matches, editMatch{
				editIndex:  i,
				matchIndex: idx,
				matchLen:   len(edit.OldText),
				newText:    edit.NewText,
			})
		}
	}

	// Phase 2: detect overlaps among non-replace_all edits
	nonReplaceAll := make([]editMatch, 0, len(matches))
	for i, m := range matches {
		if !args.Edits[m.editIndex].ReplaceAll {
			nonReplaceAll = append(nonReplaceAll, matches[i])
		}
	}
	if len(nonReplaceAll) > 1 {
		sort.Slice(nonReplaceAll, func(i, j int) bool {
			return nonReplaceAll[i].matchIndex < nonReplaceAll[j].matchIndex
		})
		for i := 1; i < len(nonReplaceAll); i++ {
			prev := nonReplaceAll[i-1]
			curr := nonReplaceAll[i]
			if prev.matchIndex+prev.matchLen > curr.matchIndex {
				return ErrorResult(fmt.Sprintf("edits[%d] and edits[%d] overlap in %s. Merge them into a single edit that covers the combined range.",
					prev.editIndex, curr.editIndex, args.Path)), nil
			}
		}
	}

	// Phase 3: apply edits
	// Process replace_all edits first (they use strings.ReplaceAll on the whole content),
	// then apply single-match edits in reverse order to preserve offsets.
	result := original

	// Apply replace_all edits
	for _, edit := range args.Edits {
		if edit.ReplaceAll {
			result = strings.ReplaceAll(result, edit.OldText, edit.NewText)
		}
	}

	// Collect single-match edits with positions in the (potentially modified) content
	var singleEdits []editMatch
	for i, edit := range args.Edits {
		if edit.ReplaceAll {
			continue
		}
		idx := strings.Index(result, edit.OldText)
		if idx < 0 {
			// Could have been consumed by a replace_all edit
			return ErrorResult(fmt.Sprintf("edits[%d]: old_text no longer found after applying replace_all edits. Check for conflicts between edits.", i)), nil
		}
		singleEdits = append(singleEdits, editMatch{
			editIndex:  i,
			matchIndex: idx,
			matchLen:   len(edit.OldText),
			newText:    edit.NewText,
		})
	}

	// Apply single edits in reverse order to preserve offsets
	sort.Slice(singleEdits, func(i, j int) bool {
		return singleEdits[i].matchIndex > singleEdits[j].matchIndex
	})
	for _, m := range singleEdits {
		result = result[:m.matchIndex] + m.newText + result[m.matchIndex+m.matchLen:]
	}

	if result == original {
		return ErrorResult("edits produced no changes"), nil
	}

	if err := os.WriteFile(path, []byte(result), 0644); err != nil {
		return ToolResult{}, fmt.Errorf("write edited file: %w", err)
	}

	// Build summary
	var summary strings.Builder
	fmt.Fprintf(&summary, "edited %s: %d edit(s) applied", args.Path, len(args.Edits))
	for i, edit := range args.Edits {
		prefix := original[:strings.Index(original, edit.OldText)]
		lineNum := strings.Count(prefix, "\n") + 1
		oldLines := strings.Count(edit.OldText, "\n") + 1
		newLines := strings.Count(edit.NewText, "\n") + 1
		if edit.ReplaceAll {
			count := strings.Count(original, edit.OldText)
			fmt.Fprintf(&summary, "\n  [%d] replaced %d occurrences (%d→%d lines each), first at line %d",
				i, count, oldLines, newLines, lineNum)
		} else {
			fmt.Fprintf(&summary, "\n  [%d] replaced %d→%d lines at line %d",
				i, oldLines, newLines, lineNum)
		}
	}

	return TextResult(summary.String()), nil
}

func (t *WriteTool) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(t.workDir, path)
}
