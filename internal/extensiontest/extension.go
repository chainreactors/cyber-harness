// Package extensiontest constructs owned hosts for tests. Production packages
// must construct their explicit profiles instead.
package extensiontest

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
	toolsext "github.com/chainreactors/aiscan/pkg/exts/tools"
)

func Set(t testing.TB, entries ...extension.Entry) *extension.Set {
	t.Helper()
	s, err := extension.New(entries...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

func Load(t testing.TB, ctx context.Context, value extension.Extension) *extension.Set {
	t.Helper()
	s := Set(t, extension.Entry{ID: "test", Extension: value})
	if err := s.Load(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func Tools(t testing.TB, values ...tool.Tool) tool.Executor {
	t.Helper()
	if len(values) == 0 {
		return tool.EmptyExecutor()
	}
	e, err := toolsext.New(values...)
	if err != nil {
		t.Fatal(err)
	}
	return Load(t, context.Background(), e).Executor()
}
