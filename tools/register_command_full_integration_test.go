//go:build full && integration

//	Run with: AISCAN_INTEGRATION=1 FOFA_EMAIL=... FOFA_KEY=... \
//	  go test -tags 'full integration' ./tools/... -run TestIntegration -v
package tools

import (
	"bytes"
	"context"
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
	passivecmd "github.com/chainreactors/aiscan/tools/passive"
	"github.com/chainreactors/aiscan/tools/scan/engine"
)

func passiveExecString(t *testing.T, cmd *passivecmd.Command, ctx context.Context, args []string) string {
	t.Helper()
	var output bytes.Buffer
	if _, err := cmd.Run(ctx, &commands.Execution{Args: args, Stdout: &output, Stderr: &output}); err != nil {
		t.Fatalf("Execute(%v) error = %v", args, err)
	}
	return output.String()
}

func TestIntegrationPassiveFofa(t *testing.T) {
	if os.Getenv("AISCAN_INTEGRATION") == "" {
		t.Skip("set AISCAN_INTEGRATION=1 to run")
	}
	email := os.Getenv("FOFA_EMAIL")
	key := os.Getenv("FOFA_KEY")
	if email == "" || key == "" {
		t.Skip("FOFA_EMAIL / FOFA_KEY required")
	}
	set := &engine.Set{}
	set.SetupUncover(engine.ReconOptions{FofaEmail: email, FofaKey: key, Limit: 5}, telemetry.NopLogger())
	if set.Uncover == nil {
		t.Fatal("expected Uncover engine to be initialized")
	}
	cmd := passivecmd.New(set.Uncover)
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
	if os.Getenv("AISCAN_INTEGRATION") == "" {
		t.Skip("set AISCAN_INTEGRATION=1 to run")
	}
	token := os.Getenv("HUNTER_TOKEN")
	apikey := os.Getenv("HUNTER_API_KEY")
	if token == "" && apikey == "" {
		t.Skip("HUNTER_TOKEN or HUNTER_API_KEY required")
	}
	set := &engine.Set{}
	set.SetupUncover(engine.ReconOptions{
		HunterToken:  token,
		HunterAPIKey: apikey,
		IngressProxy: os.Getenv("RECON_PROXY"),
		Limit:        3,
	}, telemetry.NopLogger())
	if set.Uncover == nil {
		t.Fatal("expected Uncover engine to be initialized")
	}
	cmd := passivecmd.New(set.Uncover)
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
