//go:build full

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	_ "github.com/chainreactors/aiscan/tools/katana"
	passivecmd "github.com/chainreactors/aiscan/tools/passive"
	"github.com/chainreactors/aiscan/tools/scan/engine"
	"github.com/projectdiscovery/uncover/sources"
)

func TestFullScannerFunctionalRegression(t *testing.T) {
	httpServer := newScannerHTTPFixture(t)
	bus := eventbus.New[output.ToolDataEvent]()
	recorder := newFunctionalRecorder(bus)
	registry := commands.NewRegistry()
	engineSet := &engine.Set{}
	deps := &commands.Deps{
		WorkDir: t.TempDir(),
		DataBus: bus,
		Logger:  telemetry.NopLogger(),
	}
	commands.Provide(deps, engine.SetKey, engineSet)
	commands.BuildPlan(capability.Select(capability.Options{Groups: []string{"scanner"}}), deps, registry)

	passiveEngine := &functionalPassiveEngine{}
	passive := passivecmd.New(passiveEngine).WithLogger(telemetry.NopLogger())
	registry.Register(commands.Command{Name: passive.Name(), Usage: passive.Usage(), Run: passive.Run}, "")

	for _, name := range []string{"katana", "passive"} {
		if !registry.Has(name) {
			t.Fatalf("full scanner registry missing %q; registered=%v", name, registry.GroupNames("scanner"))
		}
	}

	cases := []functionalCase{
		{
			Name: "katana/depth-and-js-crawl", Tool: "katana",
			Args:    []string{"-u", httpServer.URL, "-d", "2", "-jc", "-j", "-c", "1", "-p", "1", "-rl", "20"},
			Timeout: 30 * time.Second,
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, "/admin", "/app.js", "/api/status")
				requireEvent(t, result, "katana", output.ToolDataWeb, func(data any) bool {
					encoded, err := json.Marshal(data)
					return err == nil && strings.Contains(string(encoded), "/admin")
				})
			},
		},
		{
			Name: "passive/fofa-query-and-shape", Tool: "passive",
			Args: []string{"-s", "fofa", "domain=\"example.test\""},
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, "\"ip\":\"192.0.2.10\"", "\"port\":\"443\"", "\"title\":\"Regression Asset\"")
				if len(passiveEngine.queries) != 1 || passiveEngine.queries[0] != "fofa:domain=\"example.test\"" {
					t.Fatalf("passive queries = %#v", passiveEngine.queries)
				}
			},
		},
	}
	requireFunctionalCoverage(t, registry, cases, "scan", "gogo", "spray", "zombie", "neutron", "proton")
	runFunctionalCases(t, registry, recorder, cases)
}

type functionalPassiveEngine struct {
	queries []string
}

func (e *functionalPassiveEngine) Sources() []string { return []string{"fofa"} }

func (e *functionalPassiveEngine) QueryRaw(ctx context.Context, source, query string) ([]sources.Result, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	e.queries = append(e.queries, source+":"+query)
	raw, _ := json.Marshal(engine.RawFofa{
		IP: "192.0.2.10", Port: "443", Host: "https://example.test",
		Domain: "example.test", Title: "Regression Asset", ICP: "TEST-ICP",
	})
	return []sources.Result{{
		Source: source,
		IP:     "192.0.2.10",
		Port:   443,
		Host:   "example.test",
		Raw:    raw,
	}}, nil
}
