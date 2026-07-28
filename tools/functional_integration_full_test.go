//go:build full && integration

package tools

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	_ "github.com/chainreactors/aiscan/tools/katana"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func TestFullScannerPublicIntegration(t *testing.T) {
	if os.Getenv("AISCAN_INTEGRATION") != "1" {
		t.Skip("set AISCAN_INTEGRATION=1 to run public network regression tests")
	}

	bus := eventbus.New[output.ToolDataEvent]()
	recorder := newFunctionalRecorder(bus)
	registry := commands.NewRegistry()
	deps := &commands.Deps{WorkDir: t.TempDir(), DataBus: bus, Logger: telemetry.NopLogger()}
	commands.Provide(deps, engine.SetKey, &engine.Set{})
	commands.BuildPlan(capability.Select(capability.Options{Groups: []string{"scanner"}}), deps, registry)

	runFunctionalCases(t, registry, recorder, []functionalCase{{
		Name: "katana/redhaze-depth-one", Tool: "katana",
		Args: []string{
			"-u", "https://redhaze.top", "-d", "1", "-j", "-c", "1", "-p", "1",
			"-rl", "2", "-mdp", "8", "-timeout", "10",
		},
		Timeout: 45 * time.Second,
		Check: func(t *testing.T, result functionalResult) {
			requireOutputContains(t, result, "https://redhaze.top")
			requireEvent(t, result, "katana", output.ToolDataWeb, func(data any) bool {
				encoded, err := json.Marshal(data)
				return err == nil && strings.Contains(string(encoded), "redhaze.top")
			})
		},
	}})
}
