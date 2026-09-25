package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/truncate"
	types "github.com/chainreactors/cyber/core/types"
)

type CompactConfig struct {
	Provider           Provider
	Model              string
	KeepRecentTokens   int
	ReserveTokens      int
	MaxTokens          int
	CustomInstructions string
	PromptResolver     prompt.Resolver
	Logger             telemetry.Logger
}

type CompactResult struct {
	TokensBefore int
	TokensAfter  int
	KeptMessages int
}

func (a *Agent) Compact(ctx context.Context, cfg CompactConfig) (*CompactResult, error) {
	a.mu.Lock()
	msgs := append([]*aop.Message(nil), a.state.Messages...)
	em := a.Cfg.emitter
	if cfg.Provider == nil {
		cfg.Provider = a.Cfg.Provider
	}
	if cfg.Model == "" {
		cfg.Model = a.Cfg.Model
	}
	if cfg.KeepRecentTokens <= 0 {
		cfg.KeepRecentTokens = a.Cfg.Compaction.KeepRecentTokens
	}
	if cfg.ReserveTokens <= 0 {
		cfg.ReserveTokens = a.Cfg.Compaction.ReserveTokens
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = a.Cfg.MaxTokens
	}
	if cfg.PromptResolver == nil {
		cfg.PromptResolver = a.Cfg.PromptResolver
	}
	if cfg.Logger == nil {
		cfg.Logger = a.Cfg.Logger
	}
	a.mu.Unlock()

	em.status(types.CompactStateStart, nil)
	newMsgs, result, err := compactHistory(ctx, cfg, msgs)
	if err != nil {
		em.status(types.CompactStateError, &types.CompactDetail{Error: err.Error()})
		return nil, err
	}

	a.mu.Lock()
	a.state.Messages = newMsgs
	a.mu.Unlock()

	em.status(types.CompactStateEnd, &types.CompactDetail{TokensBefore: uint64(max(result.TokensBefore, 0)), TokensAfter: uint64(max(result.TokensAfter, 0)), KeptMessages: uint64(max(result.KeptMessages, 0))})
	return result, nil
}

func compactHistory(ctx context.Context, cfg CompactConfig, msgs []*aop.Message) ([]*aop.Message, *CompactResult, error) {
	if len(msgs) < 2 {
		return nil, nil, fmt.Errorf("nothing to compact (too few messages)")
	}
	if cfg.Provider == nil {
		return nil, nil, fmt.Errorf("compact provider is nil")
	}
	if cfg.KeepRecentTokens <= 0 {
		cfg.KeepRecentTokens = DefaultKeepRecentTokens
	}
	if cfg.ReserveTokens <= 0 {
		cfg.ReserveTokens = DefaultCompactionReserve
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = DefaultMaxTokens
	}

	tokensBefore := estimateAllTokens(msgs)
	cut := findCompactionCut(msgs, cfg.KeepRecentTokens)
	if cut.FirstKept <= 0 {
		return nil, nil, fmt.Errorf("nothing to compact (context already fits in %d tokens)", cfg.KeepRecentTokens)
	}

	summaryLimit := cfg.ReserveTokens * 4 / 5
	if summaryLimit < 1 {
		summaryLimit = 1
	}
	if summaryLimit > cfg.MaxTokens {
		summaryLimit = cfg.MaxTokens
	}
	var summary string
	if cut.SplitTurn {
		summary = "No prior history."
		if cut.TurnStart > 0 {
			var err error
			summary, err = summarizeConversation(ctx, cfg.Provider, cfg.Model, cfg.PromptResolver, cfg.Logger, prompt.CompactRequest, msgs[:cut.TurnStart], cfg.CustomInstructions, summaryLimit)
			if err != nil {
				return nil, nil, fmt.Errorf("compact history summarize: %w", err)
			}
		}
		prefixLimit := cfg.ReserveTokens / 2
		if prefixLimit < 1 {
			prefixLimit = 1
		}
		if prefixLimit > cfg.MaxTokens {
			prefixLimit = cfg.MaxTokens
		}
		prefixSummary, err := summarizeConversation(
			ctx, cfg.Provider, cfg.Model, cfg.PromptResolver, cfg.Logger, prompt.CompactPrefix, msgs[cut.TurnStart:cut.FirstKept], "", prefixLimit,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("compact turn prefix summarize: %w", err)
		}
		summary += "\n\n---\n\n**Turn Context (split turn):**\n\n" + prefixSummary
	} else {
		var err error
		summary, err = summarizeConversation(ctx, cfg.Provider, cfg.Model, cfg.PromptResolver, cfg.Logger, prompt.CompactRequest, msgs[:cut.FirstKept], cfg.CustomInstructions, summaryLimit)
		if err != nil {
			return nil, nil, fmt.Errorf("compact summarize: %w", err)
		}
	}

	summaryMsg := provider.TextMessage("user",
		"The conversation history before this point was compacted into the following summary:\n\n<summary>\n"+
			summary+"\n</summary>")
	newMsgs := make([]*aop.Message, 0, 1+len(msgs)-cut.FirstKept)
	newMsgs = append(newMsgs, summaryMsg)
	newMsgs = append(newMsgs, msgs[cut.FirstKept:]...)
	tokensAfter := estimateAllTokens(newMsgs)
	if tokensAfter >= tokensBefore {
		return nil, nil, fmt.Errorf("compaction did not reduce context (%d -> %d tokens)", tokensBefore, tokensAfter)
	}
	return newMsgs, &CompactResult{
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
		KeptMessages: len(msgs) - cut.FirstKept,
	}, nil
}

func estimateMessageTokens(msg *aop.Message) int {
	chars := 0
	for _, part := range msg.GetContent() {
		switch value := part.Value.(type) {
		case *aop.Content_Text:
			chars += len(value.Text.Text)
		case *aop.Content_Reasoning:
			chars += len(value.Reasoning.Text)
		case *aop.Content_Media:
			chars += 4800
		case *aop.Content_ToolCall:
			chars += len(value.ToolCall.Name) + len(value.ToolCall.GetArguments().GetData())
		case *aop.Content_ToolResult:
			for _, block := range value.ToolResult.Output {
				if text := block.GetText(); text != nil {
					chars += len(text.Text)
				} else if block.GetMedia() != nil {
					chars += 4800
				}
			}
		}
	}
	if chars == 0 {
		return 0
	}
	return (chars + 3) / 4
}

func estimateAllTokens(msgs []*aop.Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateMessageTokens(m)
	}
	return total
}

type compactionCut struct {
	FirstKept int
	TurnStart int
	SplitTurn bool
}

func isCompactionCutPoint(msg *aop.Message) bool {
	return (msg.Role == "user" && provider.MessageToolResult(msg) == nil) || msg.Role == "assistant"
}

// findCompactionCut walks backward to retain approximately keepTokens. A cut
// may land at a user turn boundary or at an assistant message inside a single
// oversized turn, but never at a tool result.
func findCompactionCut(msgs []*aop.Message, keepTokens int) compactionCut {
	valid := make([]int, 0, len(msgs))
	for i := range msgs {
		if isCompactionCutPoint(msgs[i]) {
			valid = append(valid, i)
		}
	}
	if len(valid) == 0 {
		return compactionCut{}
	}

	cutIdx := -1
	accumulated := 0
	reachedBudget := false
	for i := len(msgs) - 1; i >= 0; i-- {
		accumulated += estimateMessageTokens(msgs[i])
		if accumulated >= keepTokens {
			for _, candidate := range valid {
				if candidate >= i && candidate > 0 {
					cutIdx = candidate
					break
				}
			}
			reachedBudget = true
			break
		}
	}
	if !reachedBudget {
		return compactionCut{}
	}
	if cutIdx <= 0 {
		return compactionCut{}
	}
	if msgs[cutIdx].Role == "user" && provider.MessageToolResult(msgs[cutIdx]) == nil {
		return compactionCut{FirstKept: cutIdx, TurnStart: cutIdx}
	}
	for i := cutIdx - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && provider.MessageToolResult(msgs[i]) == nil {
			return compactionCut{FirstKept: cutIdx, TurnStart: i, SplitTurn: true}
		}
	}
	return compactionCut{}
}

// findCutPoint is kept as the simple index helper used by trigger checks.
func findCutPoint(msgs []*aop.Message, keepTokens int) int {
	return findCompactionCut(msgs, keepTokens).FirstKept
}

func serializeMessages(msgs []*aop.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		content := provider.MessageText(m)
		switch m.Role {
		case "user":
			if provider.MessageToolResult(m) != nil {
				continue
			}
			fmt.Fprintf(&sb, "[User]: %s\n\n", content)
		case "assistant":
			if content != "" {
				fmt.Fprintf(&sb, "[Assistant]: %s\n\n", content)
			}
			for _, call := range provider.MessageToolCalls(m) {
				fmt.Fprintf(&sb, "[Tool Call]: %s(%s)\n\n",
					call.Name, truncate.Clip(string(call.GetArguments().GetData()), 200))
			}
		case "tool":
			fmt.Fprintf(&sb, "[Tool Result]: %s\n\n", truncate.Clip(content, 500))
		case "system":
			fmt.Fprintf(&sb, "[System]: %s\n\n", truncate.Clip(content, 300))
		}
	}
	return sb.String()
}

func summarizeConversation(ctx context.Context, p Provider, model string, resolver prompt.Resolver, logger telemetry.Logger, target prompt.Target, msgs []*aop.Message, customInstructions string, maxTokens int) (string, error) {
	if resolver == nil {
		return "", fmt.Errorf("compact prompt resolver is nil")
	}
	input := prompt.Context{Target: prompt.CompactSystem}
	systemResult := resolver.Build(ctx, input)
	input.Target = target
	input.Compaction.CustomInstructions = customInstructions
	requestResult := resolver.Build(ctx, input)
	if logger != nil {
		for _, diagnostic := range append(systemResult.Diagnostics, requestResult.Diagnostics...) {
			logger.Warnf("prompt contribution=%q section=%q: %s", diagnostic.Contribution, diagnostic.Section, diagnostic.Message)
		}
	}
	systemPrompt, requestPrompt := systemResult.Prompt, requestResult.Prompt
	userContent := "<conversation>\n" + serializeMessages(msgs) + "</conversation>\n\n" + requestPrompt

	temp := float64(0)
	resp, err := p.ChatCompletion(ctx, &ChatCompletionRequest{
		Model: model,
		Messages: []*aop.Message{
			provider.TextMessage("system", systemPrompt),
			provider.TextMessage("user", userContent),
		},
		MaxTokens:   maxTokens,
		Temperature: &temp,
	})
	if err != nil {
		return "", fmt.Errorf("LLM call: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices returned")
	}
	choice := resp.Choices[0]
	if isOutputLimitFinishReason(choice.FinishReason) {
		return "", fmt.Errorf("summary output truncated (finish_reason=%s)", choice.FinishReason)
	}
	content := provider.MessageText(choice.Message)
	if content == "" {
		return "", fmt.Errorf("empty summary returned")
	}
	return content, nil
}
