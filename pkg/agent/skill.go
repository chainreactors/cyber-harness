package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
)

type SkillResult struct {
	Status      string `json:"status"`
	Target      string `json:"target"`
	Summary     string `json:"summary"`
	Detail      string `json:"detail"`
	Remediation string `json:"remediation,omitempty"`
}

type SkillOption struct {
	SkillBody string
	MaxTokens int
	Tools     *command.CommandRegistry
	Base      Config
}

func RunSkill(ctx context.Context, prompt string, opts SkillOption) (*SkillResult, error) {
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultSkillMaxTokens
	}

	tools := opts.Tools
	if tools == nil {
		tools = command.NewRegistry()
	}

	cfg := opts.Base
	cfg.Tools = tools
	cfg.SystemPrompt = buildSkillSystemPrompt(opts.SkillBody)
	cfg.MaxTokens = maxTokens
	cfg.ResponseFormat = &provider.ResponseFormat{Type: "json_object"}

	result, err := cfg.Run(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return ParseSkillResult(result.Output)
}

func ParseSkillResult(output string) (*SkillResult, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return &SkillResult{Status: "inconclusive", Summary: "empty response"}, nil
	}

	output = stripFences(output)

	var result SkillResult
	if json.Valid([]byte(output)) && json.Unmarshal([]byte(output), &result) == nil {
		result.Status = normalizeSkillStatus(result.Status)
		return &result, nil
	}
	return parseSkillFallback(output), nil
}

func buildSkillSystemPrompt(skillBody string) string {
	var sb strings.Builder
	if skillBody != "" {
		sb.WriteString(skillBody)
		sb.WriteString("\n\n")
	}
	sb.WriteString(`Return your analysis as a JSON object with these fields:
{"status":"confirmed|info|not_confirmed|inconclusive","target":"<host or URL>","summary":"<one sentence>","detail":"<supporting evidence>","remediation":"<fix advice or empty>"}

Only output the JSON object. Do not add markdown fences or extra text.`)
	return sb.String()
}

func parseSkillFallback(output string) *SkillResult {
	result := &SkillResult{Status: "inconclusive"}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "status":
			result.Status = normalizeSkillStatus(value)
		case "summary":
			result.Summary = value
		case "detail", "evidence":
			result.Detail = value
		case "target":
			result.Target = value
		case "remediation":
			result.Remediation = value
		}
	}
	if result.Summary == "" && result.Detail == "" {
		result.Summary = truncate(oneLine(output), 200)
	}
	return result
}

func normalizeSkillStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "confirmed":
		return "confirmed"
	case "not_confirmed", "not confirmed", "false_positive":
		return "not_confirmed"
	case "info", "informational":
		return "info"
	default:
		return "inconclusive"
	}
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```json") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	} else if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}

func oneLine(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.Join(strings.Fields(value), " ")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
