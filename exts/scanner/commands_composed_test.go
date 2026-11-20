package scanner

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	toolpb "github.com/chainreactors/cyber/aop/tool"
	katanaext "github.com/chainreactors/cyber/exts/katana"
	passiveext "github.com/chainreactors/cyber/exts/passive"
	"github.com/chainreactors/cyber/tools/katana"
	"github.com/chainreactors/cyber/tools/passive"
)

func TestComposedScannerCommandsAreInstalled(t *testing.T) {
	reg := installScanner(t, t.TempDir(), Config{Crawler: katana.Crawl}, katanaext.New(), passiveext.New(passive.Config{FofaKey: "deadbeef"})).commands
	for _, name := range []string{"katana", "passive"} {
		if !reg.Has(name) {
			t.Fatalf("missing %s", name)
		}
	}
}

func TestComposedScannerFunctionalRegression(t *testing.T) {
	httpServer := newScannerHTTPFixture(t)
	installed := installScanner(t, t.TempDir(), Config{Crawler: katana.Crawl}, katanaext.New(), passiveext.New(passive.Config{FofaKey: "deadbeef"}))
	registry := installed.commands
	recorder := newFunctionalRecorder(installed.events)

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
	}
	requireFunctionalCoverage(t, registry, cases, "curl", "scan", "gogo", "spray", "zombie", "neutron", "proton", "passive")
	runFunctionalCases(t, registry, recorder, cases)
}
