package agent

import (
	"testing"

	coretool "github.com/chainreactors/cyber/core/tool"
)

func newTestTools(t testing.TB, tools ...coretool.Tool) coretool.Executor {
	t.Helper()
	return testTools(t, tools...)
}
