package scan

import (
	"context"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type Option func(*Command)

type AIFunc func(ctx context.Context, prompt, systemPrompt, model string, maxTokens int) (string, error)

type AISkillConfig struct {
	Model      string
	Timeout    int
	Workers    int
	Enable     bool
	VerifyMode string
}

type SkillBodyLoader interface {
	LoadBody(name string) string
}

func WithAIFunc(fn AIFunc) Option {
	return func(c *Command) { c.aiFunc = fn }
}

func WithReportFunc(fn AIFunc) Option {
	return func(c *Command) { c.reportFunc = fn }
}

func WithAISkillConfig(cfg AISkillConfig) Option {
	return func(c *Command) { c.aiConfig = cfg }
}

func WithSkillStore(store SkillBodyLoader) Option {
	return func(c *Command) { c.skillStore = store }
}

func WithProxy(proxy string) Option {
	return func(c *Command) { c.proxy = proxy }
}

func WithLogger(logger telemetry.Logger) Option {
	return func(c *Command) {
		if logger != nil {
			c.logger = logger
		}
	}
}

func (c *Command) Configure(opts ...Option) {
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
}

// AgentRunResult carries both raw LLM output (for recording/debugging) and parsed skill result.
type AgentRunResult struct {
	Raw    string
	Parsed *agent.SkillResult
}

// AgentFunc runs a multi-turn agent with tool access and returns both raw output and parsed result.
type AgentFunc func(ctx context.Context, prompt, systemPrompt, model string, maxTokens int) (*AgentRunResult, error)

func WithAgentFunc(fn AgentFunc) Option {
	return func(c *Command) { c.agentFunc = fn }
}

func verificationEnabled(mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	return mode != "" && mode != "off"
}
