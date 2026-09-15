package agent

import (
	"testing"

	coretool "github.com/chainreactors/aiscan/core/tool"
)

func newTestTools(t testing.TB, tools ...coretool.Tool) coretool.Executor {
	t.Helper()
	return testTools(t, tools...)
}
