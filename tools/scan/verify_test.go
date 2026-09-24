package scan

import (
	"context"
	"errors"
	"testing"

	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/utils/parsers"
)

func TestScannerPassesDelegateSelectedLoots(t *testing.T) {
	for _, name := range []string{"verify", "sniper"} {
		t.Run(name, func(t *testing.T) {
			kind := parsers.LootFingerprint
			if name == "verify" {
				kind = parsers.LootVuln
			}
			coll := &collector{loots: []parsers.Loot{
				{Target: "http://chosen.test", Kind: kind, Priority: "high", Data: map[string]any{"focus": true}},
				{Target: "http://other.test", Priority: "low"},
			}}
			calls := 0
			worker := func(_ context.Context, got string, loot parsers.Loot) (string, error) {
				calls++
				if got != name || loot.Target != "http://chosen.test" {
					t.Fatalf("%s: %v", got, loot)
				}
				if name == "sniper" {
					return "status:info", nil
				}
				return "status:confirmed", nil
			}
			if name == "verify" {
				runVerifyPass(t.Context(), worker, coll, telemetry.NopLogger())
			} else {
				runSniperPass(t.Context(), worker, coll, telemetry.NopLogger())
			}
			if calls != 1 {
				t.Fatalf("calls=%d loots=%v", calls, coll.loots)
			}
			if name == "verify" && coll.loots[0].Data["verification_status"] != "confirmed" {
				t.Fatalf("verification was not recorded: %v", coll.loots[0].Data)
			}
			if name == "sniper" && (coll.loots[0].Data["research_status"] != "info" || coll.loots[0].Data["verification_status"] != nil) {
				t.Fatalf("research was not recorded: %v", coll.loots[0].Data)
			}
		})
	}
}
func TestWorkerFailureRecordsInconclusive(t *testing.T) {
	coll := &collector{loots: []parsers.Loot{{Kind: parsers.LootVuln, Priority: "high"}}}
	runVerifyPass(t.Context(), func(context.Context, string, parsers.Loot) (string, error) {
		return "status:confirmed", errors.New("failed")
	}, coll, telemetry.NopLogger())
	if coll.loots[0].Data["verification_status"] != "inconclusive" {
		t.Fatal("failed task did not record inconclusive verification")
	}
}

func TestVerifyIncludesAllVulnerabilitiesAndWeakPasswords(t *testing.T) {
	coll := &collector{}
	for _, severity := range []string{"critical", "high", "medium", "low", "info", ""} {
		for _, kind := range []string{parsers.LootVuln, parsers.LootWeakpass, parsers.LootFingerprint} {
			coll.loots = append(coll.loots, parsers.Loot{Kind: kind, Priority: severity})
		}
	}
	calls := 0
	runVerifyPass(t.Context(), func(_ context.Context, _ string, loot parsers.Loot) (string, error) {
		if loot.Kind == parsers.LootFingerprint {
			t.Fatal("fingerprint verified as a vulnerability")
		}
		calls++
		return "status:confirmed | target:localhost | evidence\nDetails", nil
	}, coll, telemetry.NopLogger())
	if calls != 12 {
		t.Fatalf("verified %d findings, want 12", calls)
	}
}

func TestVerificationRequiresAnExplicitUnambiguousVerdict(t *testing.T) {
	for _, output := range []string{"not confirmed", "unconfirmed", "the confirmed issue", "status:unknown", "status:confirmed\nstatus:not_confirmed", "status:confirmed\nstatus:confirmed", "status:info"} {
		if got := parseStatus(output, "confirmed", "not_confirmed", "inconclusive"); got != "" {
			t.Errorf("%q yielded %q", output, got)
		}
	}
	for _, status := range []string{"confirmed", "not_confirmed", "inconclusive"} {
		if got := parseStatus("status:"+status+" | target:localhost", "confirmed", "not_confirmed", "inconclusive"); got != status {
			t.Errorf("%s: %s", status, got)
		}
	}
}
