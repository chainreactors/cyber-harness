package scan

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/utils/parsers"
)

type indexedLoot struct {
	index int
	loot  parsers.Loot
}

func runVerifyPass(ctx context.Context, worker Worker, coll *collector, logger telemetry.Logger) {
	coll.mu.Lock()
	var candidates []indexedLoot
	for i, loot := range coll.loots {
		if loot.Kind == parsers.LootVuln || loot.Kind == parsers.LootWeakpass {
			candidates = append(candidates, indexedLoot{i, loot})
		}
	}
	coll.mu.Unlock()
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			break
		}
		output, err := worker(ctx, "verify", candidate.loot)
		status := parseStatus(output, "confirmed", "not_confirmed", "inconclusive")
		if err == nil && status == "" {
			err = fmt.Errorf("missing or invalid verification status")
		}
		if err != nil {
			status = "inconclusive"
		}
		coll.mu.Lock()
		annotateLoot(&coll.loots[candidate.index], status)
		if err != nil {
			coll.errors = append(coll.errors, fmt.Sprintf("verify %s: %v", candidate.loot.Target, err))
		}
		coll.mu.Unlock()
		logger.Infof("verify: %s → %s", candidate.loot.Description, status)
	}
}

func runSniperPass(ctx context.Context, worker Worker, coll *collector, logger telemetry.Logger) {
	if worker == nil {
		return
	}

	coll.mu.Lock()
	candidates := filterFingerprintLoots(coll.loots)
	coll.mu.Unlock()

	if len(candidates) == 0 {
		logger.Debugf("sniper pass: no fingerprint loots")
		return
	}

	logger.Infof("sniper pass: %d fingerprint candidates", len(candidates))

	for _, c := range candidates {
		if ctx.Err() != nil {
			break
		}
		output, err := worker(ctx, "sniper", c.loot)
		status := parseStatus(output, "info", "not_confirmed", "inconclusive")
		if err == nil && status == "" {
			err = fmt.Errorf("missing or invalid research status")
		}
		if err != nil {
			status = "inconclusive"
		}
		coll.mu.Lock()
		if coll.loots[c.index].Data == nil {
			coll.loots[c.index].Data = make(map[string]any)
		}
		coll.loots[c.index].Data["research_status"] = status
		if err != nil {
			coll.errors = append(coll.errors, fmt.Sprintf("sniper %s: %v", c.loot.Target, err))
		}
		coll.mu.Unlock()
		logger.Infof("sniper: %s → %s", c.loot.Description, status)
	}
}

func filterFingerprintLoots(loots []parsers.Loot) []indexedLoot {
	var out []indexedLoot
	for i, l := range loots {
		if l.Kind == parsers.LootFingerprint {
			focus, _ := l.Data["focus"].(bool)
			if focus {
				out = append(out, indexedLoot{index: i, loot: l})
			}
		}
	}
	return out
}

func annotateLoot(loot *parsers.Loot, status string) {
	if loot.Data == nil {
		loot.Data = make(map[string]any)
	}
	loot.Data["verification_status"] = status
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
