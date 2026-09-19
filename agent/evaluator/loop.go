package evaluator

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	types "github.com/chainreactors/cyber/core/types"
)

// defaultRoundCeiling is a runaway backstop, not a budget. The evaluator ends
// the loop by returning Continue=false, which is how a normal goal finishes;
// the ceiling only catches a loop that would otherwise never say stop.
const defaultRoundCeiling = 20

// maxConsecutiveEvalErrors bounds the one case the evaluator cannot stop
// itself: if the judge call keeps failing there is no verdict to act on, so the
// loop gives up instead of re-running the agent against a dead evaluator.
const maxConsecutiveEvalErrors = 3

type EvalLoopConfig struct {
	Evaluator *Evaluator
	// Budget bounds the loop: its Ceiling is the hard backstop (zero takes
	// defaultRoundCeiling), its Guidance is the user's own words about how long
	// to keep going, which only the evaluator can act on. Reaching the ceiling
	// means the evaluator never stopped on its own — a warning, not a normal exit.
	Budget       Budget
	Goal         string
	Criteria     string
	TurnID       string
	InitialInput *aop.Message
}

// NewLoopConfig builds an EvalLoopConfig around a fresh Evaluator. The rounds
// spec is a number, natural language, or empty for the default ceiling; see
// ParseBudget.
func NewLoopConfig(p provider.Provider, model string, logger telemetry.Logger, prompts prompt.Resolver, goal, criteria, rounds string) EvalLoopConfig {
	return newLoopConfig(p, model, logger, prompts, goal, agent.TextInput(goal), criteria, rounds)
}

// NewLoopConfigWithInput preserves transport controls and multimodal parts on
// the first evaluation round. Boundaries that already published the user input
// use this constructor so the original multimodal input is preserved in Goal mode.
func NewLoopConfigWithInput(p provider.Provider, model string, logger telemetry.Logger, prompts prompt.Resolver, input *aop.Message, criteria, rounds string) EvalLoopConfig {
	return newLoopConfig(p, model, logger, prompts, strings.TrimSpace(provider.MessageText(input)), input, criteria, rounds)
}

func newLoopConfig(p provider.Provider, model string, logger telemetry.Logger, prompts prompt.Resolver, goal string, input *aop.Message, criteria, rounds string) EvalLoopConfig {
	return EvalLoopConfig{
		Evaluator:    New(Config{Provider: p, Model: model, Logger: logger, Prompts: prompts}),
		Budget:       ParseBudget(rounds),
		Goal:         goal,
		Criteria:     criteria,
		InitialInput: input,
	}
}

func RunWithEval(ctx context.Context, a *agent.Agent, cfg EvalLoopConfig, opts ...agent.RunOption) (*agent.Result, *Verdict, error) {
	if cfg.Budget.Ceiling <= 0 {
		cfg.Budget.Ceiling = defaultRoundCeiling
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
	if input == nil {
		return nil, nil, fmt.Errorf("evaluation initial input is required")
	}
	var (
		lastVerdict *Verdict
		history     []Round
		evalErrors  int
	)
	for round := 1; round <= cfg.Budget.Ceiling; round++ {
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

		a.EmitStatus(types.EvalStateStart, &types.EvalDetail{Round: uint32(round), MaxRounds: uint32(cfg.Budget.Ceiling)}, cfg.TurnID)

		verdict, evalErr := cfg.Evaluator.Evaluate(ctx, Request{
			Goal:          cfg.Goal,
			Criteria:      cfg.Criteria,
			Messages:      result.Messages,
			Output:        result.Output,
			Turns:         result.Turns,
			ContextTokens: result.ContextTokens,
			Round:         round,
			Ceiling:       cfg.Budget.Ceiling,
			Guidance:      cfg.Budget.Guidance,
			History:       history,
		})

		if evalErr != nil {
			evalErrors++
			cfg.Evaluator.cfg.Logger.Warnf("evaluate error (round %d): %s", round, evalErr)
			a.EmitStatus(types.EvalStateError, &types.EvalDetail{Round: uint32(round), MaxRounds: uint32(cfg.Budget.Ceiling), Error: evalErr.Error()}, cfg.TurnID)
			if evalErrors >= maxConsecutiveEvalErrors || round == cfg.Budget.Ceiling {
				return finish(result), lastVerdict, evalErr
			}
			feedback := fmt.Sprintf("Evaluation could not determine if the task is complete. Original criteria: %s. Please review your work and continue if the goal is not yet fully achieved.", cfg.Criteria)
			input = agent.TextInput(feedback)
			continue
		}
		evalErrors = 0

		lastVerdict = verdict
		a.EmitStatus(types.EvalStateEnd, &types.EvalDetail{Round: uint32(round), MaxRounds: uint32(cfg.Budget.Ceiling), Pass: verdict.Pass, Reason: verdict.Reason}, cfg.TurnID)
		cfg.Evaluator.cfg.Logger.Importantf("evaluate round %d: pass=%v continue=%v inherit_context=%v reason=%q", round, verdict.Pass, verdict.Continue, verdict.InheritContext, verdict.Reason)

		if verdict.Pass {
			return finish(result), verdict, nil
		}
		// The evaluator, not a round counter, decides when an unfinished goal is
		// done being worked on.
		if !verdict.Continue {
			cfg.Evaluator.cfg.Logger.Importantf("evaluate: stopping after round %d, evaluator sees no further progress: %s", round, verdict.Reason)
			return finish(result), verdict, nil
		}
		if round == cfg.Budget.Ceiling {
			cfg.Evaluator.cfg.Logger.Warnf("evaluate: round ceiling %d reached while the evaluator still wanted another round: %s", cfg.Budget.Ceiling, verdict.Reason)
			return finish(result), verdict, nil
		}

		history = append(history, Round{Number: round, Pass: verdict.Pass, Reason: verdict.Reason, Feedback: verdict.Feedback})

		feedback := verdict.Feedback
		if feedback == "" {
			feedback = fmt.Sprintf("Not achieved: %s. Please continue.", verdict.Reason)
		}

		if !verdict.InheritContext {
			cfg.Evaluator.cfg.Logger.Importantf("evaluate: compacting context (round %d)", round)
			if _, err := a.Compact(ctx, agent.CompactConfig{
				Provider:       cfg.Evaluator.cfg.Provider,
				Model:          cfg.Evaluator.cfg.Model,
				PromptResolver: cfg.Evaluator.cfg.Prompts,
				Logger:         cfg.Evaluator.cfg.Logger,
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
