// Package filetools publishes bounded text tools backed by a borrowed files.FS.
// The instance owns tool registrations; FS owns filesystem policy and resources.
package filetools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/files"
	"github.com/chainreactors/aiscan/pkg/toolset/registry"
)

// Extension owns only its registrations, and borrows the Registry and FS.
// Both must be declared as lifecycle dependencies by its profile.
type Extension struct {
	mu         sync.Mutex
	registry   tool.Registrar
	files      *files.FS
	owner      string
	attempted  bool
	registered bool
	closed     bool
}

var _ extension.Extension = (*Extension)(nil)

func New(r tool.Registrar, filesystem *files.FS, owner string) (*Extension, error) {
	if r == nil || filesystem == nil || strings.TrimSpace(owner) == "" {
		return nil, fmt.Errorf("file tools require a registry, file service and owner")
	}
	return &Extension{registry: r, files: filesystem, owner: owner}, nil
}

func (m *Extension) Load(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.attempted && !m.registered {
		return registry.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.registered {
		return nil
	}
	m.attempted = true
	if !m.files.Ready() {
		return files.ErrUnavailable
	}
	tools := []tool.Tool{&fileTool{files: m.files}}
	tools = append(tools, &listingTool{files: m.files}, &listingTool{files: m.files, glob: true})
	if !m.files.ReadOnly() {
		tools = append(tools, &fileTool{files: m.files, write: true})
	}
	if err := m.registry.Register(m.owner, tools...); err != nil {
		return err
	}
	m.registered = true
	return nil
}

func (m *Extension) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	if m.registered {
		if err := m.registry.UnregisterOwner(ctx, m.owner); err != nil {
			return errors.Join(extension.ErrCloseIncomplete, err)
		}
	}
	m.closed = true
	return nil
}

type readArgs struct {
	Path   string `json:"path" jsonschema:"description=Relative file path inside the configured directory, or a path under an explicitly mounted URI prefix"`
	Offset int    `json:"offset,omitempty" jsonschema:"description=First line to return, starting at 1"`
	Limit  int    `json:"limit,omitempty" jsonschema:"description=Maximum number of lines to return"`
}

type writeArgs struct {
	Path    string            `json:"path" jsonschema:"description=Relative file path inside the configured directory"`
	Content *string           `json:"content,omitempty" jsonschema:"description=UTF-8 content to write"`
	Edits   []files.EditPatch `json:"edits,omitempty" jsonschema:"description=Targeted replacements against original content"`
}

type fileTool struct {
	files *files.FS
	write bool
}

func (t *fileTool) Name() string {
	if t.write {
		return "write"
	}
	return "read"
}

func (t *fileTool) Description() string {
	if t.write {
		return "Write a bounded UTF-8 text file inside the configured directory."
	}
	return "Read a bounded UTF-8 text file inside the configured directory or an explicitly selected read-only mount."
}

func (t *fileTool) Definition() *tool.Definition {
	if t.write {
		return tool.Def(t.Name(), t.Description(), writeArgs{})
	}
	return tool.Def(t.Name(), t.Description(), readArgs{})
}

func (t *fileTool) Execute(ctx context.Context, arguments string) (*tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.write {
		args, err := tool.ParseArgs[writeArgs](arguments)
		if err != nil {
			return nil, err
		}
		if len(args.Edits) > 0 {
			if err := t.files.Edit(ctx, args.Path, args.Edits); err != nil {
				return nil, err
			}
			return tool.TextResult(fmt.Sprintf("Edited %s: %d edits", args.Path, len(args.Edits))), nil
		}
		if args.Content == nil || !utf8.ValidString(*args.Content) {
			return nil, fmt.Errorf("write requires UTF-8 content or edits")
		}
		if err := t.files.Write(ctx, args.Path, []byte(*args.Content)); err != nil {
			return nil, err
		}
		return tool.TextResult(fmt.Sprintf("Wrote %d bytes to %s", len(*args.Content), args.Path)), nil
	}
	args, err := tool.ParseArgs[readArgs](arguments)
	if err != nil {
		return nil, err
	}
	data, err := t.files.Read(ctx, args.Path)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("read requires UTF-8 text")
	}
	if args.Offset < 0 || args.Limit < 0 {
		return nil, fmt.Errorf("offset and limit must be nonnegative")
	}
	if args.Offset > 0 || args.Limit > 0 {
		lines := strings.Split(string(data), "\n")
		start := max(args.Offset-1, 0)
		if start >= len(lines) {
			return tool.TextResult(""), nil
		}
		end := len(lines)
		if args.Limit > 0 {
			end = start + min(args.Limit, len(lines)-start)
		}
		return tool.TextResult(strings.Join(lines[start:end], "\n")), nil
	}
	return tool.TextResult(string(data)), nil
}
