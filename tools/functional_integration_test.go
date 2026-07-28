//go:build integration

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	_ "github.com/chainreactors/aiscan/tools/gogo"
	_ "github.com/chainreactors/aiscan/tools/neutron"
	"github.com/chainreactors/aiscan/tools/scan/engine"
	_ "github.com/chainreactors/aiscan/tools/spray"
	"github.com/chainreactors/utils/parsers"
)

func TestScannerPublicIntegration(t *testing.T) {
	if os.Getenv("AISCAN_INTEGRATION") != "1" {
		t.Skip("set AISCAN_INTEGRATION=1 to run public network regression tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	engineSet, err := engine.InitWithOptions(ctx, resources.Options{}, telemetry.NopLogger())
	if err != nil {
		t.Fatalf("initialize scanner engines: %v", err)
	}
	defer engineSet.Close()

	bus := eventbus.New[output.ToolDataEvent]()
	recorder := newFunctionalRecorder(bus)
	registry := commands.NewRegistry()
	deps := &commands.Deps{
		WorkDir: t.TempDir(),
		DataBus: bus, Logger: telemetry.NopLogger(),
	}
	commands.Provide(deps, engine.SetKey, engineSet)
	commands.Provide(deps, resources.SetKey, engineSet.Resources)
	commands.BuildPlan(capability.Select(capability.Options{Groups: []string{"scanner"}}), deps, registry)
	templateFile := filepath.Join(t.TempDir(), "redhaze-marker.yaml")
	writeTestFile(t, templateFile, `id: redhaze-public-marker
info:
  name: RedHaze public regression marker
  severity: info
  tags: regression
http:
  - method: GET
    path:
      - '{{BaseURL}}'
    matchers:
      - type: word
        words:
          - 'RedHaze Group'
`)

	cases := []functionalCase{
		{
			Name: "gogo/redhaze-http-https-fingerprint", Tool: "gogo",
			Args:    []string{"-i", "redhaze.top", "-p", "80,443", "-v", "-o", "jl", "-t", "2"},
			Timeout: 45 * time.Second,
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, `"port":"80"`, `"port":"443"`, "nginx")
				requireEvent(t, result, "gogo", output.ToolDataService, func(data any) bool {
					item, ok := data.(*parsers.GOGOResult)
					return ok && item != nil && item.Port == "443" && item.Protocol == "https"
				})
			},
		},
		{
			Name: "spray/redhaze-explicit-https", Tool: "spray",
			Args:    []string{"-u", "https://redhaze.top", "-j", "--limit", "1", "--timeout", "10"},
			Timeout: 30 * time.Second,
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, `"url":"https://redhaze.top`, `"status":301`, "nginx")
				if strings.Contains(result.Stdout, `"url":"http://redhaze.top`) {
					t.Fatalf("spray downgraded explicit HTTPS target:\n%s", result.Stdout)
				}
			},
		},
		{
			Name: "neutron/redhaze-benign-template", Tool: "neutron",
			Args: []string{
				"-i", "https://id.redhaze.top/home", "-t", templateFile,
				"--tags", "regression", "-s", "info", "--concurrency", "1",
				"--rate-limit", "1", "--timeout", "20", "-j",
			},
			Timeout: 30 * time.Second,
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, `"matched":true`, `"template":"redhaze-public-marker"`)
				requireEvent(t, result, "neutron", output.ToolDataVuln, nil)
			},
		},
		{
			Name: "scan/redhaze-limited-pipeline", Tool: "scan",
			Args: []string{
				"-i", "redhaze.top", "--ports", "80,443", "--mode", "quick",
				"--verify=off", "--timeout", "8", "--no-color",
			},
			Timeout: 90 * time.Second,
			Check: func(t *testing.T, result functionalResult) {
				requireOutputContains(t, result, "[summary] completed", "443", "nginx")
				requireEvent(t, result, "gogo", output.ToolDataService, func(data any) bool {
					item, ok := data.(*parsers.GOGOResult)
					return ok && item != nil && item.Port == "443" && item.Protocol == "https"
				})
			},
		},
	}
	runFunctionalCases(t, registry, recorder, cases)
}
