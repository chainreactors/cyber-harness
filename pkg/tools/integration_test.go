//go:build full && integration

//	Run with: AISCAN_INTEGRATION=1 FOFA_EMAIL=... FOFA_KEY=... \
//	  go test -tags 'full integration' ./pkg/tools/... -run TestIntegration -v
package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	passivecmd "github.com/chainreactors/aiscan/pkg/tools/passive"
	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
)

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
	out, err := cmd.Execute(ctx, []string{"-s", "fofa", `domain="anthropic.com"`})
	if err != nil {
		t.Fatalf("passive fofa Execute: %v", err)
	}
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
	out, err := cmd.Execute(ctx, []string{"-s", "hunter", `domain.suffix="anthropic.com"`})
	if err != nil {
		t.Fatalf("passive hunter Execute: %v", err)
	}
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
