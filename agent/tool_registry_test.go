package agent

import (
	"context"
	"github.com/chainreactors/aiscan/core/extension"
	coretool "github.com/chainreactors/aiscan/core/tool"
	toolsext "github.com/chainreactors/aiscan/pkg/exts/tools"
	"testing"
)

func newTestTools(t testing.TB, tools ...coretool.Tool) coretool.Executor {
	t.Helper()
	if len(tools) == 0 {
		return coretool.EmptyExecutor()
	}
	e, err := toolsext.New(tools...)
	if err != nil {
		t.Fatal(err)
	}
	s, err := extension.New(extension.Entry{ID: "tools", Extension: e})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s.Executor()
}
