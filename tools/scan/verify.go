package scan

import (
	"context"
	"strings"

	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/utils/parsers"
)

type indexedLoot struct {
	index int
	loot  parsers.Loot
}

func runVerifyPass(ctx context.Context, worker Worker, coll *collector, level priority, logger telemetry.Logger) {
	if worker == nil {
		return
	}

	coll.mu.Lock()
	candidates := filterLootsByPriority(coll.loots, level)
	coll.mu.Unlock()

	if len(candidates) == 0 {
		logger.Debugf("verify pass: no loots at or above %s", level)
		return
	}

	logger.Infof("verify pass: %d candidates at or above %s", len(candidates), level)

	for _, c := range candidates {
		if ctx.Err() != nil {
			break
		}
		result := runWorker(ctx, worker, "verify", c.loot, logger)
		if result != nil {
			coll.mu.Lock()
			annotateLoot(&coll.loots[c.index], result.Status)
			coll.mu.Unlock()
			logger.Infof("verify: %s → %s", c.loot.Description, result.Status)
		}
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
		result := runWorker(ctx, worker, "sniper", c.loot, logger)
		if result != nil {
			coll.mu.Lock()
			annotateLoot(&coll.loots[c.index], result.Status)
			coll.mu.Unlock()
			logger.Infof("sniper: %s → %s", c.loot.Description, result.Status)
		}
	}
}

type verifyResult struct {
	Status string
}

func runWorker(ctx context.Context, worker Worker, name string, loot parsers.Loot, logger telemetry.Logger) *verifyResult {
	output, err := worker(ctx, name, loot)
	if err != nil {
		logger.Warnf("%s agent error: %s", name, err)
		return nil
	}
	status := parseVerifyStatus(output)
	if status == "" {
		return nil
	}
	return &verifyResult{Status: status}
}

func filterLootsByPriority(loots []parsers.Loot, min priority) []indexedLoot {
	var out []indexedLoot
	for i, l := range loots {
		if priority(l.Priority).atLeast(min) {
			out = append(out, indexedLoot{index: i, loot: l})
		}
	}
	return out
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
	loot.Data["verification_status"] = normalizeStatus(status)
}

func normalizeStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "confirmed":
		return "confirmed"
	case "not_confirmed", "not confirmed", "false_positive":
		return "not_confirmed"
	case "info", "informational":
		return "info"
	case "inconclusive":
		return "inconclusive"
	default:
		return ""
	}
}

func parseVerifyStatus(output string) string {
	if i := strings.Index(output, "status:"); i >= 0 {
		rest := output[i+len("status:"):]
		end := strings.IndexAny(rest, " |\t\n\r")
		if end < 0 {
			end = len(rest)
		}
		if s := normalizeStatus(rest[:end]); s != "" {
			return s
		}
	}
	lower := strings.ToLower(output)
	for _, candidate := range []string{"not_confirmed", "confirmed", "inconclusive", "info"} {
		if strings.Contains(lower, candidate) {
			return candidate
		}
	}
	return ""
}
