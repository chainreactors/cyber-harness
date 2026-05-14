package scan

import (
	"context"
	"strings"

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

func (c *Command) applyVerificationDefaults(flags *flags, args []string) {
	if flags == nil {
		return
	}
	if c.verification.Enable && !hasFlag(args, "--verify") {
		minPriority := strings.TrimSpace(c.verification.MinPriority)
		if minPriority == "" {
			minPriority = "high"
		}
		flags.Verify = minPriority
	}
	if !hasFlag(args, "--verify-timeout") && c.verification.Timeout > 0 {
		flags.VerifyTimeout = c.verification.Timeout
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
