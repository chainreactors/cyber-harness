package scan

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/utils/parsers"
)

func runVerifyPass(ctx context.Context, worker Worker, coll *collector, logger telemetry.Logger) {
	coll.mu.Lock()
	loots := append([]parsers.Loot(nil), coll.loots...)
	coll.mu.Unlock()
	for i, loot := range loots {
		if loot.Kind != parsers.LootVuln && loot.Kind != parsers.LootWeakpass {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		output, err := worker(ctx, "verify", loot)
		status := parseStatus(output, "confirmed", "not_confirmed", "inconclusive")
		if err == nil && status == "" {
			err = fmt.Errorf("missing or invalid verification status")
		}
		if err != nil {
			status = "inconclusive"
		}
		coll.mu.Lock()
		if coll.loots[i].Data == nil {
			coll.loots[i].Data = make(map[string]any)
		}
		coll.loots[i].Data["verification_status"] = status
		if err != nil {
			coll.errors = append(coll.errors, fmt.Sprintf("verify %s: %v", loot.Target, err))
		}
		coll.mu.Unlock()
		logger.Infof("verify: %s → %s", loot.Description, status)
	}
}

func runSniperPass(ctx context.Context, worker Worker, coll *collector, logger telemetry.Logger) {
	if worker == nil {
		return
	}

	coll.mu.Lock()
	loots := append([]parsers.Loot(nil), coll.loots...)
	coll.mu.Unlock()
	var candidates []int
	for i, loot := range loots {
		focus, _ := loot.Data["focus"].(bool)
		if loot.Kind == parsers.LootFingerprint && focus {
			candidates = append(candidates, i)
		}
	}

	if len(candidates) == 0 {
		logger.Debugf("sniper pass: no fingerprint loots")
		return
	}

	logger.Infof("sniper pass: %d fingerprint candidates", len(candidates))

	for _, i := range candidates {
		if ctx.Err() != nil {
			break
		}
		loot := loots[i]
		output, err := worker(ctx, "sniper", loot)
		status := parseStatus(output, "info", "not_confirmed", "inconclusive")
		if err == nil && status == "" {
			err = fmt.Errorf("missing or invalid research status")
		}
		if err != nil {
			status = "inconclusive"
		}
		coll.mu.Lock()
		if coll.loots[i].Data == nil {
			coll.loots[i].Data = make(map[string]any)
		}
		coll.loots[i].Data["research_status"] = status
		if err != nil {
			coll.errors = append(coll.errors, fmt.Sprintf("sniper %s: %v", loot.Target, err))
		}
		coll.mu.Unlock()
		logger.Infof("sniper: %s → %s", loot.Description, status)
	}
}

func parseStatus(output string, allowed ...string) string {
	var status string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "status:") {
			continue
		}
		if status != "" {
			return ""
		}
		token := strings.TrimSpace(strings.SplitN(strings.TrimPrefix(line, "status:"), "|", 2)[0])
		if !slices.Contains(allowed, token) {
			return ""
		}
		status = token
	}
	return status
}
