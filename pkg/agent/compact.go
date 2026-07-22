package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent/truncate"
	xcompact "github.com/chainreactors/aiscan/pkg/aop/x/compact"
)

const defaultKeepRecentTokens = 20000

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

type CompactConfig struct {
	Provider           Provider
	Model              string
	KeepRecentTokens   int
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
	a.mu.Unlock()

	if len(msgs) < 4 {
		return nil, fmt.Errorf("nothing to compact (too few messages)")
	}
	if cfg.KeepRecentTokens <= 0 {
		cfg.KeepRecentTokens = defaultKeepRecentTokens
	}

	em.status(xcompact.StateStart, "", nil)

	tokensBefore := estimateAllTokens(msgs)
	cutIdx := findCutPoint(msgs, cfg.KeepRecentTokens)
	if cutIdx <= 0 {
		em.status(xcompact.StateError, xcompact.NS, xcompact.Detail{Error: "context already fits"})
		return nil, fmt.Errorf("nothing to compact (context already fits in %d tokens)", cfg.KeepRecentTokens)
	}

	summary, err := summarize(ctx, cfg.Provider, cfg.Model, msgs[:cutIdx], cfg.CustomInstructions)
	if err != nil {
		em.status(xcompact.StateError, xcompact.NS, xcompact.Detail{Error: err.Error()})
		return nil, fmt.Errorf("compact summarize: %w", err)
	}

	summaryMsg := NewTextMessage("user",
		"The conversation history before this point was compacted into the following summary:\n\n<summary>\n"+
			summary+"\n</summary>")
	newMsgs := make([]ChatMessage, 0, 1+len(msgs)-cutIdx)
	newMsgs = append(newMsgs, summaryMsg)
	newMsgs = append(newMsgs, msgs[cutIdx:]...)

	result := &CompactResult{
		TokensBefore: tokensBefore,
		TokensAfter:  estimateAllTokens(newMsgs),
		KeptMessages: len(msgs) - cutIdx,
	}

	a.mu.Lock()
	a.state.Messages = newMsgs
	a.mu.Unlock()

	em.status(xcompact.StateEnd, xcompact.NS, xcompact.Detail{TokensBefore: result.TokensBefore, TokensAfter: result.TokensAfter, KeptMessages: result.KeptMessages})
	return result, nil
}

func estimateMessageTokens(msg ChatMessage) int {
	chars := 0
	if msg.Content != nil {
		chars += len(*msg.Content)
	}
	for _, part := range msg.ContentParts {
		chars += len(part.Text)
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

// findCutPoint returns the index of the first message to keep.
// Messages before this index are summarized; messages from this index onward are retained.
// Returns 0 if all messages fit within keepTokens (nothing to compact).
func findCutPoint(msgs []ChatMessage, keepTokens int) int {
	accumulated := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		accumulated += estimateMessageTokens(msgs[i])
		if accumulated >= keepTokens {
			for j := i; j < len(msgs); j++ {
				if msgs[j].Role == "user" && msgs[j].ToolCallID == "" {
					return j
				}
			}
			return i
		}
	}
	return 0
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

func summarize(ctx context.Context, p Provider, model string, msgs []ChatMessage, customInstructions string) (string, error) {
	prompt := compactUserPrompt
	if customInstructions != "" {
		prompt += "\n\nAdditional focus: " + customInstructions
	}
	userContent := "<conversation>\n" + serializeMessages(msgs) + "</conversation>\n\n" + prompt

	temp := float64(0)
	resp, err := p.ChatCompletion(ctx, &ChatCompletionRequest{
		Model: model,
		Messages: []ChatMessage{
			NewTextMessage("system", compactSystemPrompt),
			NewTextMessage("user", userContent),
		},
		MaxTokens:   4096,
		Temperature: &temp,
	})
	if err != nil {
		return "", fmt.Errorf("LLM call: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices returned")
	}
	content := resp.Choices[0].Message.Content
	if content == nil || *content == "" {
		return "", fmt.Errorf("empty summary returned")
	}
	return *content, nil
}
