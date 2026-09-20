package scan

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/utils/parsers"
)

type indexedLoot struct {
	index int
	loot  parsers.Loot
}

func runVerifyPass(ctx context.Context, parent *agent.Agent, readSkill func(string) string, coll *collector, level priority, logger telemetry.Logger) {
	if readSkill == nil {
		return
	}
	skillPrompt := readSkill("verify")
	if skillPrompt == "" {
		logger.Debugf("verify pass: skill content not available, skipping")
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
		result := runVerifyAgent(ctx, parent, skillPrompt, c.loot, logger)
		if result != nil {
			coll.mu.Lock()
			annotateLoot(&coll.loots[c.index], result.Status)
			coll.mu.Unlock()
			logger.Infof("verify: %s → %s", c.loot.Description, result.Status)
		}
	}
}

func runSniperPass(ctx context.Context, parent *agent.Agent, readSkill func(string) string, coll *collector, logger telemetry.Logger) {
	if readSkill == nil {
		return
	}
	skillPrompt := readSkill("sniper")
	if skillPrompt == "" {
		logger.Debugf("sniper pass: skill content not available, skipping")
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
		result := runSniperAgent(ctx, parent, skillPrompt, c.loot, logger)
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

func runVerifyAgent(ctx context.Context, parent *agent.Agent, skillPrompt string, loot parsers.Loot, logger telemetry.Logger) *verifyResult {
	sub := parent.Derive()
	sub.Cfg = sub.Cfg.WithSystemPromptFunc(workerPrompt(VerifySystemTarget, skillPrompt, logger)).WithStream(false)
	request, err := resolveWorkerPrompt(ctx, sub.Cfg.PromptResolver, prompt.Context{
		Target: VerifyRequestTarget,
		Agent: prompt.AgentContext{
			Name: sub.Cfg.AgentName, Model: sub.Cfg.Model, Instructions: skillPrompt,
		},
		Payload: WorkerPromptPayload{Loot: loot},
	}, logger)
	if err != nil {
		logger.Debugf("verify prompt error: %s", err)
		return nil
	}

	r, err := sub.Run(ctx, agent.TextInput(request))
	if err != nil {
		logger.Debugf("verify agent error: %s", err)
		return nil
	}
	status := parseVerifyStatus(r.Output)
	if status == "" {
		return nil
	}
	return &verifyResult{Status: status}
}

func runSniperAgent(ctx context.Context, parent *agent.Agent, skillPrompt string, loot parsers.Loot, logger telemetry.Logger) *verifyResult {
	sub := parent.Derive()
	sub.Cfg = sub.Cfg.WithSystemPromptFunc(workerPrompt(SniperSystemTarget, skillPrompt, logger)).WithStream(false)
	request, err := resolveWorkerPrompt(ctx, sub.Cfg.PromptResolver, prompt.Context{
		Target: SniperRequestTarget,
		Agent: prompt.AgentContext{
			Name: sub.Cfg.AgentName, Model: sub.Cfg.Model, Instructions: skillPrompt,
		},
		Payload: WorkerPromptPayload{Loot: loot},
	}, logger)
	if err != nil {
		logger.Debugf("sniper prompt error: %s", err)
		return nil
	}

	r, err := sub.Run(ctx, agent.TextInput(request))
	if err != nil {
		logger.Debugf("sniper agent error: %s", err)
		return nil
	}
	status := parseVerifyStatus(r.Output)
	if status == "" {
		return nil
	}
	return &verifyResult{Status: status}
}

func workerPrompt(target prompt.Target, instructions string, logger telemetry.Logger) agent.SystemPromptFunc {
	return func(ctx context.Context, config *agent.Config) (string, error) {
		if config == nil || config.PromptResolver == nil {
			return "", fmt.Errorf("scanner prompt resolver is unavailable")
		}
		input := prompt.Context{Target: target, Agent: prompt.AgentContext{Instructions: instructions}}
		input.Agent.Name, input.Agent.Model = config.AgentName, config.Model
		return resolveWorkerPrompt(ctx, config.PromptResolver, input, logger)
	}
}

func resolveWorkerPrompt(ctx context.Context, resolver prompt.Resolver, input prompt.Context, logger telemetry.Logger) (string, error) {
	if resolver == nil {
		return "", fmt.Errorf("scanner prompt resolver is unavailable")
	}
	result := resolver.Build(ctx, input)
	if logger != nil {
		for _, diagnostic := range result.Diagnostics {
			logger.Warnf("prompt contribution=%q section=%q: %s", diagnostic.Contribution, diagnostic.Section, diagnostic.Message)
		}
	}
	if strings.TrimSpace(result.Prompt) == "" {
		return "", fmt.Errorf("scanner prompt target %q is unavailable", input.Target)
	}
	return result.Prompt, nil
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
