package scan

import (
	"context"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type Option func(*Command)

type DeepBrowserFunc func(ctx context.Context, targetURL string) (string, error)

func WithParent(a *agent.Agent) Option {
	return func(c *Command) { c.parent = a }
}

func WithProxy(proxy string) Option {
	return func(c *Command) { c.Proxy = proxy }
}

func WithLogger(logger telemetry.Logger) Option {
	return func(c *Command) { c.InitLogger(logger) }
}

func (c *Command) Configure(opts ...Option) {
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
}

func WithDeepBrowserFunc(fn DeepBrowserFunc) Option {
	return func(c *Command) { c.deepBrowser = fn }
}

// SkillReader reads a scan sub-skill by name (e.g. "verify", "sniper", "deep").
// Returns the skill content or "" if not found.
type SkillReader func(name string) string

func WithSkillReader(r SkillReader) Option {
	return func(c *Command) { c.readSkill = r }
}
