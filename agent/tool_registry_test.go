package agent

import (
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	"testing"
)

func newTestTools(t testing.TB, tools ...coretool.Tool) coretool.Executor {
	t.Helper()
	return hosttest.Tools(t, tools...)
}
