//go:build full && integration

//	Run with: CYBER_INTEGRATION=1 FOFA_KEY=... \
//	  go test -tags 'full integration' ./pkg/exts/scanner -run TestIntegration -v
package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	toolpb "github.com/chainreactors/cyber/aop/tool"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/scan/engine"
)

func passiveExecString(t *testing.T, registry *coretool.CommandRegistry, ctx context.Context, args []string) string {
	t.Helper()
	var output bytes.Buffer
	if _, err := registry.Run(ctx, append([]string{"passive"}, args...), &coretool.Execution{Stdout: &output, Stderr: &output}); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func TestIntegrationPassiveFofa(t *testing.T) {
	if os.Getenv("CYBER_INTEGRATION") == "" {
		t.Skip("set CYBER_INTEGRATION=1 to run")
	}
	key := os.Getenv("FOFA_KEY")
	if key == "" {
		t.Skip("FOFA_KEY required")
	}
	cmd := installScanner(t, t.TempDir(), Config{Recon: engine.ReconOptions{FofaKey: key, Limit: 5}}).commands
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out := passiveExecString(t, cmd, ctx, []string{"-s", "fofa", `domain="anthropic.com"`})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("no assets returned: %q", out)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON array: %v\n%s", err, out)
	}
	if got[0]["ip"] == "" {
		t.Errorf("missing ip: %+v", got[0])
	}
	t.Logf("passive/fofa returned %d assets, first=%v", len(got), got[0])
}

func TestIntegrationPassiveHunter(t *testing.T) {
	if os.Getenv("CYBER_INTEGRATION") == "" {
		t.Skip("set CYBER_INTEGRATION=1 to run")
	}
	apikey := os.Getenv("HUNTER_API_KEY")
	if apikey == "" {
		t.Skip("HUNTER_API_KEY required")
	}
	cmd := installScanner(t, t.TempDir(), Config{Recon: engine.ReconOptions{HunterAPIKey: apikey, IngressProxy: os.Getenv("RECON_PROXY"), Limit: 3}}).commands
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out := passiveExecString(t, cmd, ctx, []string{"-s", "hunter", `domain.suffix="anthropic.com"`})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Logf("hunter returned empty (may be quota/WAF); output: %q", out)
		return
	}
	t.Logf("passive/hunter output (first 500 bytes): %s", truncForTest(out, 500))
}

func truncForTest(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func TestFullScannerPublicIntegration(t *testing.T) {
	if os.Getenv("CYBER_INTEGRATION") != "1" {
		t.Skip("set CYBER_INTEGRATION=1 to run public network regression tests")
	}

	installed := installScanner(t, t.TempDir(), Config{})
	recorder := newFunctionalRecorder(installed.events)
	registry := installed.commands

	runFunctionalCases(t, registry, recorder, []functionalCase{{
		Name: "katana/redhaze-depth-one", Tool: "katana",
		Args: []string{
			"-u", "https://redhaze.top", "-d", "1", "-j", "-c", "1", "-p", "1",
			"-rl", "2", "-mdp", "8", "-timeout", "10",
		},
		Timeout: 45 * time.Second,
		Check: func(t *testing.T, result functionalResult) {
			requireOutputContains(t, result, "https://redhaze.top")
			requireEvent(t, result, "katana", toolpb.ArtifactKindWeb, func(data any) bool {
				encoded, err := json.Marshal(data)
				return err == nil && strings.Contains(string(encoded), "redhaze.top")
			})
		},
	}})
}
