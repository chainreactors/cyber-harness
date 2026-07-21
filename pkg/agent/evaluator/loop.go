package evaluator

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/pkg/agent"
)

const defaultMaxEvalRounds = 3

type EvalLoopConfig struct {
	Evaluator     *Evaluator
	MaxEvalRounds int
	Goal          string
	Criteria      string
}

func RunWithEval(ctx context.Context, a *agent.Agent, cfg EvalLoopConfig) (*agent.Result, *Verdict, error) {
	if cfg.MaxEvalRounds <= 0 {
		cfg.MaxEvalRounds = defaultMaxEvalRounds
	}

	result, err := a.Run(ctx, agent.TextInput(cfg.Goal))
	if err != nil {
		return result, nil, err
	}

	for attempt := 0; attempt < cfg.MaxEvalRounds; attempt++ {
		// Judge whenever the run produced work worth evaluating. Only bail on a
		// hard error or a user cancel — a run that merely hit its turn or token
		// budget (Stopped/Budget) still did work the criteria should be checked
		// against, and is exactly when a fresh feedback round is most useful.
		// (The old gate skipped everything but Terminated/Completed, so a
		// turn-capped agent silently never got evaluated.)
		if result.Stop == agent.StopReasonError || result.Stop == agent.StopReasonCanceled {
			return result, nil, nil
		}

		a.EmitStatus(agent.StatusEvalStart, map[string]any{"eval_round": attempt})

		verdict, evalErr := cfg.Evaluator.Evaluate(
			ctx, cfg.Goal, cfg.Criteria,
			result.Messages, result.Output, result.Turns, result.ContextTokens,
		)

		if evalErr != nil {
			cfg.Evaluator.cfg.Logger.Warnf("evaluate error (round %d): %s", attempt+1, evalErr)
			a.EmitStatus(agent.StatusEvalError, map[string]any{
				"eval_round": attempt,
				"eval_error": evalErr.Error(),
			})
			feedback := fmt.Sprintf("Evaluation could not determine if the task is complete. Original criteria: %s. Please review your work and continue if the goal is not yet fully achieved.", cfg.Criteria)
			result, err = a.Run(ctx, agent.TextInput(feedback))
			if err != nil {
				return result, nil, err
			}
			continue
		}

		a.EmitStatus(agent.StatusEvalEnd, map[string]any{
			"eval_round": attempt,
			"eval_pass":  verdict.Pass,
			"eval_reason": verdict.Reason,
		})
		cfg.Evaluator.cfg.Logger.Importantf("evaluate round %d: pass=%v inherit_context=%v reason=%q", attempt+1, verdict.Pass, verdict.InheritContext, verdict.Reason)

		if verdict.Pass {
			return result, verdict, nil
		}

		feedback := verdict.Feedback
		if feedback == "" {
			feedback = fmt.Sprintf("Not achieved: %s. Please continue.", verdict.Reason)
		}

		if !verdict.InheritContext {
			cfg.Evaluator.cfg.Logger.Importantf("evaluate: compacting context (round %d)", attempt+1)
			if _, err := a.Compact(ctx, agent.CompactConfig{
				Provider: cfg.Evaluator.cfg.Provider,
				Model:    cfg.Evaluator.cfg.Model,
			}); err != nil {
				cfg.Evaluator.cfg.Logger.Warnf("compact failed, falling back to reset: %s", err)
				a.Reset()
			}
		}

		cfg.Evaluator.cfg.Logger.Importantf("evaluate: injecting feedback (round %d): %s", attempt+1, feedback)

		result, err = a.Run(ctx, agent.TextInput(feedback))
		if err != nil {
			cfg.Evaluator.cfg.Logger.Warnf("evaluate: agent.Run failed after feedback: %s", err)
			return result, verdict, err
		}
		cfg.Evaluator.cfg.Logger.Importantf("evaluate: agent completed after feedback (round %d), stop=%s turns=%d", attempt+1, result.Stop, result.Turns)
	}

	return result, nil, nil
}
