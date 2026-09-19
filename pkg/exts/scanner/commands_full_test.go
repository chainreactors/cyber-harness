//go:build full

package scanner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	toolpb "github.com/chainreactors/cyber/aop/tool"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/tools/katana"
	passivecmd "github.com/chainreactors/cyber/tools/passive"
	"github.com/chainreactors/cyber/tools/scan/engine"
	"github.com/chainreactors/sdk/gogo"
	"github.com/chainreactors/sdk/spray"
	"github.com/projectdiscovery/uncover/sources"
)

func TestRegisterAllRegistersKatanaInFullBuild(t *testing.T) {
	gogoEng, _ := gogo.NewEngine(nil)
	sprayEng, _ := spray.NewEngine(nil)
	engineSet := &engine.Set{
		Gogo:  gogoEng,
		Spray: sprayEng,
	}
	reg := registerTestScanners(t, engineSet, t.TempDir(), nil, telemetry.NopLogger(), katana.NewCommand(telemetry.NopLogger(), "", nil))

	if !reg.Has("katana") {
		t.Fatal("expected katana to be registered in full build")
	}
}

func TestRegisterAllRegistersPassiveWithUncover(t *testing.T) {
	engineSet := &engine.Set{}
	engineSet.SetupUncover(engine.ReconOptions{
		FofaKey: "deadbeef",
	}, nil)
	reg := registerTestScanners(t, engineSet, t.TempDir(), nil, telemetry.NopLogger(), passivecmd.NewCommand(engineSet, telemetry.NopLogger()))

	if !reg.Has("passive") {
		t.Fatal("expected passive to be registered when engineSet.Uncover is non-nil")
	}
}

func TestFullScannerFunctionalRegression(t *testing.T) {
	httpServer := newScannerHTTPFixture(t)
	bus := coreevents.New()
	recorder := newFunctionalRecorder(bus)
	engineSet := &engine.Set{}
	passiveEngine := &functionalPassiveEngine{}
	passive := passivecmd.New(passiveEngine).WithLogger(telemetry.NopLogger())
	registry := registerTestScanners(t, engineSet, t.TempDir(), bus, telemetry.NopLogger(),
		katana.NewCommand(telemetry.NopLogger(), "", bus),
		commands.Command{Name: passive.Name(), Usage: passive.Usage(), Run: passive.Run},
	)

	for _, name := range []string{"katana", "passive"} {
		if !registry.Has(name) {
			t.Fatalf("full scanner registry missing %q; registered=%v", name, registry.Names())
		}
	}

	cases := []functionalCase{
		{
			Name: "katana/depth-and-js-crawl", Tool: "katana",
			Args:    []string{"-u", httpServer.URL, "-d", "2", "-jc", "-j", "-c", "1", "-p", "1", "-rl", "20"},
			Timeout: 30 * time.Second,
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, "/admin", "/app.js", "/api/status")
				requireEvent(t, result, "katana", toolpb.ArtifactKindWeb, func(data any) bool {
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
	requireFunctionalCoverage(t, registry, cases, "curl", "scan", "gogo", "spray", "zombie", "neutron", "proton")
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
