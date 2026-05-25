//go:build e2e

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Verdict is the structured result from an LLM judge evaluation.
type Verdict struct {
	Pass   bool     `json:"pass"`
	Score  int      `json:"score"`
	Reason string   `json:"reason"`
	Issues []string `json:"issues"`
}

// Judge evaluates agent execution results using an LLM.
type Judge struct {
	baseURL string
	apiKey  string
	model   string
	timeout time.Duration
}

func NewJudge(baseURL, apiKey, model string) *Judge {
	return &Judge{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		timeout: 30 * time.Second,
	}
}

func (h *Harness) Judge() *Judge {
	return NewJudge(h.baseURL, h.apiKey, h.model)
}

// Evaluate sends the intent and execution trace to the LLM for judgment.
func (j *Judge) Evaluate(intent string, criteria string, r *RunResult) (*Verdict, error) {
	trace := buildTrace(r)
	prompt := buildJudgePrompt(intent, criteria, trace)
	return j.call(prompt)
}

func buildTrace(r *RunResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Exit code: %d\n", r.ExitCode)
	fmt.Fprintf(&sb, "Duration: %s\n", r.Duration.Round(time.Millisecond))
	fmt.Fprintf(&sb, "Turns: %d\n", r.Turns())
	fmt.Fprintf(&sb, "Tool calls: %d\n", len(r.ToolCalls()))

	sb.WriteString("\nTool call trace:\n")
	for i, e := range r.ToolCalls() {
		fmt.Fprintf(&sb, "  [%d] %s", i+1, e.ToolName)
		if e.IsError {
			sb.WriteString(" (ERROR)")
		}
		sb.WriteByte('\n')
		if e.Args != "" {
			fmt.Fprintf(&sb, "      args: %s\n", clip(e.Args, 200))
		}
		if e.Result != "" {
			fmt.Fprintf(&sb, "      result: %s\n", clip(e.Result, 300))
		}
	}

	if output := strings.TrimSpace(r.Stdout); output != "" {
		fmt.Fprintf(&sb, "\nFinal output:\n%s\n", clip(output, 1000))
	}
	return sb.String()
}

const judgeSystemPrompt = `You are a strict test evaluator for an AI agent system. Given an intent (what was asked), evaluation criteria, and execution trace (what happened), determine whether the agent correctly fulfilled the intent.

Respond with ONLY a JSON object:
{"pass": true/false, "score": 0-100, "reason": "one sentence summary", "issues": ["issue1", "issue2"]}

Rules:
- pass=true only if the intent was fully and correctly completed
- score: 100=perfect, 80+=good, 60+=acceptable, <60=fail
- issues: list specific problems (empty if pass=true)
- Be strict: "ran without errors" is not the same as "fulfilled the intent"
- Check that the right tools were used with correct arguments
- Check that results contain expected data, not just that tools were called`

func buildJudgePrompt(intent, criteria, trace string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Intent\n%s\n\n", intent)
	if criteria != "" {
		fmt.Fprintf(&sb, "## Evaluation Criteria\n%s\n\n", criteria)
	}
	fmt.Fprintf(&sb, "## Execution Trace\n%s", trace)
	return sb.String()
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (j *Judge) call(userPrompt string) (*Verdict, error) {
	body := chatRequest{
		Model: j.model,
		Messages: []chatMessage{
			{Role: "system", Content: judgeSystemPrompt},
			{Role: "user", Content: userPrompt},
		},
		MaxTokens:   512,
		Temperature: 0,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), j.timeout)
	defer cancel()

	url := j.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+j.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("judge API call failed: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("judge API returned %d: %s", resp.StatusCode, clip(string(respData), 500))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respData, &chatResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("judge returned no choices")
	}

	return parseVerdict(chatResp.Choices[0].Message.Content)
}

func parseVerdict(raw string) (*Verdict, error) {
	raw = strings.TrimSpace(raw)
	raw = stripJSONFences(raw)

	var v Verdict
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("parse verdict JSON: %w\nraw: %s", err, clip(raw, 500))
	}
	return &v, nil
}

func stripJSONFences(s string) string {
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
