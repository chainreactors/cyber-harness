package evaluator

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	types "github.com/chainreactors/aiscan/pkg/types"
)

const defaultMaxEvalRounds = 3

type EvalLoopConfig struct {
	Evaluator     *Evaluator
	MaxEvalRounds int
	Goal          string
	Criteria      string
	TurnID        string
	InitialInput  *aop.Message
}

// NewLoopConfig builds an EvalLoopConfig around a fresh Evaluator. A
// maxRounds of zero (or negative) defers to RunWithEval's default.
func NewLoopConfig(p provider.Provider, model string, logger telemetry.Logger, goal, criteria string, maxRounds int) EvalLoopConfig {
	return newLoopConfig(p, model, logger, goal, agent.TextInput(goal), criteria, maxRounds)
}

// NewLoopConfigWithInput preserves transport controls and multimodal parts on
// the first evaluation round. Boundaries that already published the user input
// use this constructor so the original multimodal input is preserved in Goal mode.
func NewLoopConfigWithInput(p provider.Provider, model string, logger telemetry.Logger, input *aop.Message, criteria string, maxRounds int) EvalLoopConfig {
	return newLoopConfig(p, model, logger, strings.TrimSpace(provider.MessageText(input)), input, criteria, maxRounds)
}

func newLoopConfig(p provider.Provider, model string, logger telemetry.Logger, goal string, input *aop.Message, criteria string, maxRounds int) EvalLoopConfig {
	return EvalLoopConfig{
		Evaluator:     New(Config{Provider: p, Model: model, Logger: logger}),
		MaxEvalRounds: maxRounds,
		Goal:          goal,
		Criteria:      criteria,
		InitialInput:  input,
	}
}

func RunWithEval(ctx context.Context, a *agent.Agent, cfg EvalLoopConfig, opts ...agent.RunOption) (*agent.Result, *Verdict, error) {
	if cfg.MaxEvalRounds <= 0 {
		cfg.MaxEvalRounds = defaultMaxEvalRounds
	}
	var (
		totalUsage *aop.TokenUsage
		totalTurns int
	)
	accumulate := func(u *aop.TokenUsage) {
		if u == nil {
			return
		}
		if totalUsage == nil {
			totalUsage = &aop.TokenUsage{Detail: map[string]uint64{}}
		}
		totalUsage.InputTokens += u.InputTokens
		totalUsage.OutputTokens += u.OutputTokens
		totalUsage.TotalTokens += u.TotalTokens
		totalUsage.Detail["cache_read"] += u.Detail["cache_read"]
		totalUsage.Detail["cache_write"] += u.Detail["cache_write"]
	}
	finish := func(result *agent.Result) *agent.Result {
		if result != nil {
			result.TotalUsage = totalUsage
			result.Turns = totalTurns
		}
		return result
	}

	input := cfg.InitialInput
	// Keep direct EvalLoopConfig literals compatible with the pre-InitialInput
	// API. Constructors always populate InitialInput.
	if input == nil {
		input = agent.TextInput(cfg.Goal)
	}
	var lastVerdict *Verdict
	for round := 1; round <= cfg.MaxEvalRounds; round++ {
		result, err := a.Run(ctx, input, opts...)
		if result != nil {
			totalTurns += result.Turns
			accumulate(result.TotalUsage)
		}
		if err != nil {
			return finish(result), lastVerdict, err
		}
		// Judge whenever the run produced work worth evaluating. Only bail on a
		// hard error or a user cancel — a run that merely hit its turn or token
		// budget (Stopped/Budget) still did work the criteria should be checked
		// against, and is exactly when a fresh feedback round is most useful.
		// (The old gate skipped everything but Terminated/Completed, so a
		// turn-capped agent silently never got evaluated.)
		if result.Stop == agent.StopReasonError || result.Stop == agent.StopReasonCanceled {
			return finish(result), lastVerdict, result.Err
		}

		a.EmitStatus(types.EvalStateStart, &types.EvalDetail{Round: uint32(round), MaxRounds: uint32(max(cfg.MaxEvalRounds, 0))}, cfg.TurnID)

		verdict, evalErr := cfg.Evaluator.Evaluate(
			ctx, cfg.Goal, cfg.Criteria,
			result.Messages, result.Output, result.Turns, result.ContextTokens,
		)

		if evalErr != nil {
			cfg.Evaluator.cfg.Logger.Warnf("evaluate error (round %d): %s", round, evalErr)
			a.EmitStatus(types.EvalStateError, &types.EvalDetail{Round: uint32(round), MaxRounds: uint32(max(cfg.MaxEvalRounds, 0)), Error: evalErr.Error()}, cfg.TurnID)
			if round == cfg.MaxEvalRounds {
				return finish(result), lastVerdict, evalErr
			}
			feedback := fmt.Sprintf("Evaluation could not determine if the task is complete. Original criteria: %s. Please review your work and continue if the goal is not yet fully achieved.", cfg.Criteria)
			input = agent.TextInput(feedback)
			continue
		}

		lastVerdict = verdict
		a.EmitStatus(types.EvalStateEnd, &types.EvalDetail{Round: uint32(round), MaxRounds: uint32(max(cfg.MaxEvalRounds, 0)), Pass: verdict.Pass, Reason: verdict.Reason}, cfg.TurnID)
		cfg.Evaluator.cfg.Logger.Importantf("evaluate round %d: pass=%v inherit_context=%v reason=%q", round, verdict.Pass, verdict.InheritContext, verdict.Reason)

		if verdict.Pass {
			return finish(result), verdict, nil
		}
		if round == cfg.MaxEvalRounds {
			return finish(result), verdict, nil
		}

		feedback := verdict.Feedback
		if feedback == "" {
			feedback = fmt.Sprintf("Not achieved: %s. Please continue.", verdict.Reason)
		}

		if !verdict.InheritContext {
			cfg.Evaluator.cfg.Logger.Importantf("evaluate: compacting context (round %d)", round)
			if _, err := a.Compact(ctx, agent.CompactConfig{
				Provider: cfg.Evaluator.cfg.Provider,
				Model:    cfg.Evaluator.cfg.Model,
			}); err != nil {
				cfg.Evaluator.cfg.Logger.Warnf("compact failed, falling back to reset: %s", err)
				a.Reset()
			}
		}

		cfg.Evaluator.cfg.Logger.Importantf("evaluate: injecting feedback (round %d): %s", round, feedback)
		input = agent.TextInput(feedback)
	}
	return nil, lastVerdict, nil
}
