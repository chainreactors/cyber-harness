package files

import (
	"context"
	coretool "github.com/chainreactors/cyber/core/tool"
	"strings"
)

type listingTool struct {
	files *Files
	glob  bool
}
type listArgs struct {
	Path    string `json:"path,omitempty"`
	Pattern string `json:"pattern,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

func (t *listingTool) Name() string {
	if t.glob {
		return "glob"
	}
	return "ls"
}
func (t *listingTool) Description() string {
	if t.glob {
		return "Find paths using shell wildcards (*, ?, character classes); ** is not supported."
	}
	return "List entries in a directory inside the workspace."
}
func (t *listingTool) Definition() *coretool.Definition {
	return coretool.Def(t.Name(), t.Description(), listArgs{})
}
func (t *listingTool) Execute(ctx context.Context, arguments string) (*coretool.Result, error) {
	a, err := coretool.ParseArgs[listArgs](arguments)
	if err != nil {
		return nil, err
	}
	var names []string
	if t.glob {
		names, err = t.files.Glob(ctx, a.Pattern, a.Limit)
	} else {
		entries, listErr := t.files.List(ctx, a.Path)
		err = listErr
		limit := a.Limit
		if limit <= 0 || limit > 10000 {
			limit = 1000
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			names = append(names, name)
			if len(names) >= limit {
				break
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return coretool.TextResult(strings.Join(names, "\n")), nil
}
