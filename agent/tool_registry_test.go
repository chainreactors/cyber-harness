package agent

import (
	coretool "github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/internal/extensiontest"
	"testing"
)

func newTestTools(t testing.TB, tools ...coretool.Tool) coretool.Executor {
	t.Helper()
	return extensiontest.Tools(t, tools...)
}
