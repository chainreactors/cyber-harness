package scan

import (
	"context"
	"strings"

	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type Option func(*Command)

type AIFunc func(ctx context.Context, prompt, systemPrompt, model string, maxTokens int) (string, error)

type ReportFunc func(ctx context.Context, prompt, systemPrompt, model string, maxTokens int) (string, error)

type AISkillConfig struct {
	Model   string
	Timeout int
	Workers int
	Enable  bool
}

type SkillBodyLoader interface {
	LoadBody(name string) string
}

func WithAIFunc(fn AIFunc) Option {
	return func(c *Command) { c.aiFunc = fn }
}

func WithReportFunc(fn ReportFunc) Option {
	return func(c *Command) { c.reportFunc = fn }
}

func WithAISkillConfig(cfg AISkillConfig) Option {
	return func(c *Command) { c.aiConfig = cfg }
}

func WithSkillStore(store SkillBodyLoader) Option {
	return func(c *Command) { c.skillStore = store }
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

func verificationEnabled(mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	return mode != "" && mode != "off"
}

func hasFlag(args []string, long string) bool {
	for _, arg := range args {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			return true
		}
	}
	return false
}
