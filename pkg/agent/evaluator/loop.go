package evaluator

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	xeval "github.com/chainreactors/aiscan/pkg/aop/x/eval"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

const defaultMaxEvalRounds = 3

type EvalLoopConfig struct {
	Evaluator     *Evaluator
	MaxEvalRounds int
	Goal          string
	Criteria      string
	TurnID        string
}

// NewLoopConfig builds an EvalLoopConfig around a fresh Evaluator. A
// maxRounds of zero (or negative) defers to RunWithEval's default.
func NewLoopConfig(p provider.Provider, model string, logger telemetry.Logger, goal, criteria string, maxRounds int) EvalLoopConfig {
	return EvalLoopConfig{
		Evaluator:     New(Config{Provider: p, Model: model, Logger: logger}),
		MaxEvalRounds: maxRounds,
		Goal:          goal,
		Criteria:      criteria,
	}
}

func RunWithEval(ctx context.Context, a *agent.Agent, cfg EvalLoopConfig, opts ...agent.RunOption) (*agent.Result, *Verdict, error) {
	if cfg.MaxEvalRounds <= 0 {
		cfg.MaxEvalRounds = defaultMaxEvalRounds
	}
	var (
		totalUsage agent.Usage
		totalTurns int
	)
	finish := func(result *agent.Result) *agent.Result {
		if result != nil {
			result.TotalUsage = totalUsage
			result.Turns = totalTurns
		}
		return result
	}

	input := agent.TextInput(cfg.Goal)
	var lastVerdict *Verdict
	for round := 1; round <= cfg.MaxEvalRounds; round++ {
		result, err := a.Run(ctx, input, opts...)
		if result != nil {
			totalTurns += result.Turns
			totalUsage.PromptTokens += result.TotalUsage.PromptTokens
			totalUsage.CompletionTokens += result.TotalUsage.CompletionTokens
			totalUsage.TotalTokens += result.TotalUsage.TotalTokens
			totalUsage.CacheReadTokens += result.TotalUsage.CacheReadTokens
			totalUsage.CacheWriteTokens += result.TotalUsage.CacheWriteTokens
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

		a.EmitStatus(xeval.StateStart, xeval.NS, xeval.Detail{Round: round, MaxRounds: cfg.MaxEvalRounds}, cfg.TurnID)

		verdict, evalErr := cfg.Evaluator.Evaluate(
			ctx, cfg.Goal, cfg.Criteria,
			result.Messages, result.Output, result.Turns, result.ContextTokens,
		)

		if evalErr != nil {
			cfg.Evaluator.cfg.Logger.Warnf("evaluate error (round %d): %s", round, evalErr)
			a.EmitStatus(xeval.StateError, xeval.NS, xeval.Detail{Round: round, MaxRounds: cfg.MaxEvalRounds, Error: evalErr.Error()}, cfg.TurnID)
			if round == cfg.MaxEvalRounds {
				return finish(result), lastVerdict, evalErr
			}
			feedback := fmt.Sprintf("Evaluation could not determine if the task is complete. Original criteria: %s. Please review your work and continue if the goal is not yet fully achieved.", cfg.Criteria)
			input = agent.TextInput(feedback)
			continue
		}

		lastVerdict = verdict
		a.EmitStatus(xeval.StateEnd, xeval.NS, xeval.Detail{Round: round, MaxRounds: cfg.MaxEvalRounds, Pass: verdict.Pass, Reason: verdict.Reason}, cfg.TurnID)
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
