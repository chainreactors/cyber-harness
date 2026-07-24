package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent/truncate"
	xcompact "github.com/chainreactors/aiscan/pkg/aop/x/compact"
)

const compactSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`

const compactUserPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [File paths, function names, error messages, or other data needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const compactTurnPrefixPrompt = `This is the PREFIX of a turn that was too large to keep. The SUFFIX (recent work) is retained. Summarize the prefix to provide context for the retained suffix.

Use this EXACT format:

## Original Request
[What did the user ask for in this turn?]

## Early Progress
- [Key decisions and work done in the prefix]

## Context for Suffix
- [Information needed to understand the retained recent work]

Be concise. Focus on what is needed to understand the kept suffix.`

type CompactConfig struct {
	Provider           Provider
	Model              string
	KeepRecentTokens   int
	ReserveTokens      int
	MaxTokens          int
	CustomInstructions string
}

type CompactResult struct {
	TokensBefore int
	TokensAfter  int
	KeptMessages int
}

func (a *Agent) Compact(ctx context.Context, cfg CompactConfig) (*CompactResult, error) {
	a.mu.Lock()
	msgs := append([]ChatMessage(nil), a.state.Messages...)
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
	a.mu.Unlock()

	em.status(xcompact.StateStart, "", nil)
	newMsgs, result, err := compactHistory(ctx, cfg, msgs)
	if err != nil {
		em.status(xcompact.StateError, xcompact.NS, xcompact.Detail{Error: err.Error()})
		return nil, err
	}

	a.mu.Lock()
	a.state.Messages = newMsgs
	a.mu.Unlock()

	em.status(xcompact.StateEnd, xcompact.NS, xcompact.Detail{TokensBefore: result.TokensBefore, TokensAfter: result.TokensAfter, KeptMessages: result.KeptMessages})
	return result, nil
}

func compactHistory(ctx context.Context, cfg CompactConfig, msgs []ChatMessage) ([]ChatMessage, *CompactResult, error) {
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
			summary, err = summarize(ctx, cfg.Provider, cfg.Model, msgs[:cut.TurnStart], cfg.CustomInstructions, summaryLimit)
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
			ctx, cfg.Provider, cfg.Model, msgs[cut.TurnStart:cut.FirstKept], compactTurnPrefixPrompt, prefixLimit,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("compact turn prefix summarize: %w", err)
		}
		summary += "\n\n---\n\n**Turn Context (split turn):**\n\n" + prefixSummary
	} else {
		var err error
		summary, err = summarize(ctx, cfg.Provider, cfg.Model, msgs[:cut.FirstKept], cfg.CustomInstructions, summaryLimit)
		if err != nil {
			return nil, nil, fmt.Errorf("compact summarize: %w", err)
		}
	}

	summaryMsg := NewTextMessage("user",
		"The conversation history before this point was compacted into the following summary:\n\n<summary>\n"+
			summary+"\n</summary>")
	newMsgs := make([]ChatMessage, 0, 1+len(msgs)-cut.FirstKept)
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

func estimateMessageTokens(msg ChatMessage) int {
	chars := 0
	if msg.Content != nil {
		chars += len(*msg.Content)
	}
	for _, part := range msg.ContentParts {
		if part.Type == "image_url" {
			chars += 4800
		} else {
			chars += len(part.Text)
		}
	}
	if msg.ReasoningContent != nil {
		chars += len(*msg.ReasoningContent)
	}
	for _, tc := range msg.ToolCalls {
		chars += len(tc.Function.Name) + len(tc.Function.Arguments)
	}
	if chars == 0 {
		return 0
	}
	return (chars + 3) / 4
}

func estimateAllTokens(msgs []ChatMessage) int {
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

func isCompactionCutPoint(msg ChatMessage) bool {
	return (msg.Role == "user" && msg.ToolCallID == "") || msg.Role == "assistant"
}

// findCompactionCut walks backward to retain approximately keepTokens. A cut
// may land at a user turn boundary or at an assistant message inside a single
// oversized turn, but never at a tool result.
func findCompactionCut(msgs []ChatMessage, keepTokens int) compactionCut {
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
	if msgs[cutIdx].Role == "user" && msgs[cutIdx].ToolCallID == "" {
		return compactionCut{FirstKept: cutIdx, TurnStart: cutIdx}
	}
	for i := cutIdx - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && msgs[i].ToolCallID == "" {
			return compactionCut{FirstKept: cutIdx, TurnStart: i, SplitTurn: true}
		}
	}
	return compactionCut{}
}

// findCutPoint is kept as the simple index helper used by trigger checks.
func findCutPoint(msgs []ChatMessage, keepTokens int) int {
	return findCompactionCut(msgs, keepTokens).FirstKept
}

func serializeMessages(msgs []ChatMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		content := ""
		if m.Content != nil {
			content = *m.Content
		}
		switch m.Role {
		case "user":
			if m.ToolCallID != "" {
				continue
			}
			fmt.Fprintf(&sb, "[User]: %s\n\n", content)
		case "assistant":
			if content != "" {
				fmt.Fprintf(&sb, "[Assistant]: %s\n\n", content)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&sb, "[Tool Call]: %s(%s)\n\n",
					tc.Function.Name, truncate.Clip(tc.Function.Arguments, 200))
			}
		case "tool":
			fmt.Fprintf(&sb, "[Tool Result]: %s\n\n", truncate.Clip(content, 500))
		case "system":
			fmt.Fprintf(&sb, "[System]: %s\n\n", truncate.Clip(content, 300))
		}
	}
	return sb.String()
}

func summarize(ctx context.Context, p Provider, model string, msgs []ChatMessage, customInstructions string, maxTokens int) (string, error) {
	prompt := compactUserPrompt
	if customInstructions != "" {
		prompt += "\n\nAdditional focus: " + customInstructions
	}
	return summarizeConversation(ctx, p, model, msgs, prompt, maxTokens)
}

func summarizeConversation(ctx context.Context, p Provider, model string, msgs []ChatMessage, prompt string, maxTokens int) (string, error) {
	userContent := "<conversation>\n" + serializeMessages(msgs) + "</conversation>\n\n" + prompt

	temp := float64(0)
	resp, err := p.ChatCompletion(ctx, &ChatCompletionRequest{
		Model: model,
		Messages: []ChatMessage{
			NewTextMessage("system", compactSystemPrompt),
			NewTextMessage("user", userContent),
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
	content := choice.Message.Content
	if content == nil || *content == "" {
		return "", fmt.Errorf("empty summary returned")
	}
	return *content, nil
}

func usageTotalTokens(usage *Usage) int {
	if usage == nil {
		return 0
	}
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.PromptTokens + usage.CompletionTokens
}
