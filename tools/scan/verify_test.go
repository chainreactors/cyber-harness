package scan

import (
	"context"
	"errors"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/utils/parsers"
	"testing"
)

func TestScannerPassesDelegateSelectedLoots(t *testing.T) {
	for _, name := range []string{"verify", "sniper"} {
		t.Run(name, func(t *testing.T) {
			coll := &collector{loots: []parsers.Loot{
				{Target: "http://chosen.test", Kind: parsers.LootFingerprint, Priority: "high", Data: map[string]any{"focus": true}},
				{Target: "http://other.test", Priority: "low"},
			}}
			calls := 0
			worker := func(_ context.Context, got string, loot parsers.Loot) (string, error) {
				calls++
				if got != name || loot.Target != "http://chosen.test" {
					t.Fatalf("%s: %v", got, loot)
				}
				return "status:confirmed", nil
			}
			if name == "verify" {
				runVerifyPass(t.Context(), worker, coll, priority("high"), telemetry.NopLogger())
			} else {
				runSniperPass(t.Context(), worker, coll, telemetry.NopLogger())
			}
			if calls != 1 || coll.loots[0].Data["verification_status"] != "confirmed" || coll.loots[1].Data["verification_status"] != nil {
				t.Fatalf("calls=%d loots=%v", calls, coll.loots)
			}
		})
	}
}
func TestWorkerFailureDoesNotAnnotateLoot(t *testing.T) {
	coll := &collector{loots: []parsers.Loot{{Priority: "high"}}}
	runVerifyPass(t.Context(), func(context.Context, string, parsers.Loot) (string, error) {
		return "status:confirmed", errors.New("failed")
	}, coll, priority("high"), telemetry.NopLogger())
	if coll.loots[0].Data["verification_status"] != nil {
		t.Fatal("failed task annotated loot")
	}
}
