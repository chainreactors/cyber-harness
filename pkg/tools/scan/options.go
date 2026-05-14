package scan

import (
	"context"

	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type Option func(*Command)

type VerifyFunc func(ctx context.Context, prompt, systemPrompt, model string, maxTokens int) (string, error)

type VerificationConfig struct {
	Model        string
	Enable       bool
	MinPriority  string
	SystemPrompt string
	Timeout      int
	Workers      int
}

func WithVerificationConfig(config VerificationConfig) Option {
	return func(c *Command) {
		c.verification = config
	}
}

func WithVerifyFunc(fn VerifyFunc) Option {
	return func(c *Command) {
		c.verifyFunc = fn
	}
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
