package workspacefiles

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	filepb "github.com/chainreactors/aiscan/aop/file"
	coretool "github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/fileaudit"
	"github.com/chainreactors/aiscan/pkg/files"
)

type workspaceWriteTool struct {
	workDir string
	audit   *fileaudit.Audit
}

func newWorkspaceWriteTool(workDir string, audit *fileaudit.Audit) *workspaceWriteTool {
	return &workspaceWriteTool{workDir: workDir, audit: audit}
}

func (t *workspaceWriteTool) Name() string { return "write" }

func (t *workspaceWriteTool) Description() string {
	return "Write or edit a file. Two modes:\n" +
		"(1) Write — provide 'content' to create or overwrite a file.\n" +
		"(2) Edit — provide 'edits' array with targeted replacements. " +
		"Each edit's old_text must match exactly in the original file. " +
		"All edits are matched against the original content, not incrementally. " +
		"Do not include overlapping edits; merge them into one instead."
}

type EditPatch = files.EditPatch

type WriteArgs struct {
	Path    string      `json:"path"             jsonschema:"description=File path to write or edit (absolute or relative to working directory)"`
	Content string      `json:"content,omitempty" jsonschema:"description=Full file content for write mode. Ignored when edits is provided."`
	Edits   []EditPatch `json:"edits,omitempty"   jsonschema:"description=One or more targeted replacements. Each edit is matched against the original file. Do not include overlapping edits."`
}

func (t *workspaceWriteTool) Definition() *coretool.Definition {
	return coretool.Def("write", t.Description(), WriteArgs{})
}

func (t *workspaceWriteTool) Execute(ctx context.Context, arguments string) (*coretool.Result, error) {
	effective := *t
	effective.workDir = coretool.WorkDirFromContext(ctx, t.workDir)
	t = &effective
	args, err := coretool.ParseArgs[WriteArgs](arguments)
	if err != nil {
		return nil, err
	}

	if args.Path == "" {
		return nil, fmt.Errorf("path is required")
	}

	if len(args.Edits) > 0 {
		return t.editFile(ctx, args)
	}

	return t.writeFile(ctx, args)
}

func (t *workspaceWriteTool) writeFile(ctx context.Context, args WriteArgs) (*coretool.Result, error) {
	path := t.resolvePath(args.Path)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	// Whether the path existed decides CREATE vs WRITE, and it can only be
	// asked before the write.
	_, existed := os.Stat(path)

	if err := os.WriteFile(path, []byte(args.Content), 0644); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	op := filepb.AccessOp_ACCESS_OP_WRITE
	if existed != nil {
		op = filepb.AccessOp_ACCESS_OP_CREATE
	}
	t.audit.RecordFile(ctx, op, path, &filepb.Access{
		Size:   int64(len(args.Content)),
		Bytes:  int64(len(args.Content)),
		Digest: fileaudit.Digest([]byte(args.Content)),
	})

	lineCount := strings.Count(args.Content, "\n") + 1
	return coretool.TextResult(fmt.Sprintf("wrote %d bytes (%d lines) to %s", len(args.Content), lineCount, args.Path)), nil
}

func (t *workspaceWriteTool) editFile(ctx context.Context, args WriteArgs) (*coretool.Result, error) {
	path := t.resolvePath(args.Path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file for edit: %w", err)
	}
	original := string(data)

	result, editErr := files.ApplyEdits(original, args.Edits)
	if editErr != nil {
		return coretool.ErrorResult(editErr.Error()), nil
	}

	if err := os.WriteFile(path, []byte(result), 0644); err != nil {
		return nil, fmt.Errorf("write edited file: %w", err)
	}

	// EDIT rather than WRITE: the patch count is what distinguishes a targeted
	// change from a file the agent replaced wholesale.
	t.audit.RecordFile(ctx, filepb.AccessOp_ACCESS_OP_EDIT, path, &filepb.Access{
		Size:   int64(len(result)),
		Bytes:  int64(len(result)),
		Edits:  uint32(len(args.Edits)),
		Digest: fileaudit.Digest([]byte(result)),
	})

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

	return coretool.TextResult(summary.String()), nil
}

func (t *workspaceWriteTool) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(t.workDir, path)
}
