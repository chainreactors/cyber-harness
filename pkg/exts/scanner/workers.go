package scanner

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/subagent"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/utils/parsers"
)

func scannerSubagents(readSkill func(string) string) []subagent.Subagent {
	return []subagent.Subagent{
		scannerSubagent("verify", "Verify a security finding and report its verification status.", scan.VerifySystemTarget, scan.VerifyRequestTarget, readSkill),
		scannerSubagent("sniper", "Research public vulnerabilities for a discovered fingerprint.", scan.SniperSystemTarget, scan.SniperRequestTarget, readSkill),
	}
}

func scannerSubagent(name, description string, systemTarget, requestTarget prompt.Target, readSkill func(string) string) subagent.Subagent {
	return subagent.Subagent{Name: name, Description: description, DefaultMode: subagent.Sync,
		Prepare: func(ctx context.Context, cfg agent.Config, input subagent.Input) (agent.Config, string, error) {
			instructions := readSkill(name)
			if strings.TrimSpace(instructions) == "" {
				return cfg, "", fmt.Errorf("scanner subagent %q skill is unavailable", name)
			}
			agentContext := prompt.AgentContext{Name: cfg.AgentName, Model: cfg.Model, Instructions: instructions}
			system, err := resolveWorkerPrompt(ctx, cfg.PromptResolver, prompt.Context{
				Target: systemTarget, Agent: agentContext,
			}, cfg.Logger)
			if err != nil {
				return cfg, "", err
			}
			cfg = cfg.WithSystemPrompt(system).WithStream(false)
			if input.Payload == nil {
				if strings.TrimSpace(input.Prompt) == "" {
					return cfg, "", fmt.Errorf("scanner subagent %q requires a prompt or loot", name)
				}
				return cfg, input.Prompt, nil
			}
			payload, ok := input.Payload.(scan.WorkerPromptPayload)
			if !ok {
				return cfg, "", fmt.Errorf("scanner subagent %q requires scan.WorkerPromptPayload", name)
			}
			if strings.TrimSpace(payload.Loot.Target) == "" {
				return cfg, "", fmt.Errorf("scanner subagent %q requires a loot target", name)
			}
			request, err := resolveWorkerPrompt(ctx, cfg.PromptResolver, prompt.Context{
				Target: requestTarget, Agent: agentContext, Payload: payload,
			}, cfg.Logger)
			if err == nil && strings.TrimSpace(input.Prompt) != "" {
				request += "\n\n" + input.Prompt
			}
			return cfg, request, err
		},
	}
}

func scannerWorker(executor subagent.Executor, defaults agent.Config) scan.Worker {
	return func(ctx context.Context, name string, loot parsers.Loot) (string, error) {
		cfg := defaults
		if caller, ok := agent.ToolAgentConfig(ctx); ok {
			cfg = caller
		}
		if cfg.Provider == nil {
			return "", fmt.Errorf("scanner subagent %q requires a model provider", name)
		}
		result, err := executor.Execute(ctx, cfg, subagent.Request{Name: name, Input: subagent.Input{Payload: scan.WorkerPromptPayload{Loot: loot}}})
		if err != nil {
			return "", err
		}
		if result == nil {
			return "", nil
		}
		return result.Output, nil
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
