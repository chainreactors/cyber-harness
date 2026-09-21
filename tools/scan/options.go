package scan

import (
	"context"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/utils/parsers"
)

type invocationProxyKey struct{}

func withInvocationProxy(ctx context.Context, proxy string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, invocationProxyKey{}, proxy)
}

func (c *Command) proxyForContext(ctx context.Context) string {
	if ctx == nil {
		return c.Proxy
	}
	if proxy, ok := ctx.Value(invocationProxyKey{}).(string); ok {
		return proxy
	}
	return c.Proxy
}

type Option func(*Command)

// Worker delegates scanner-owned input without exposing agent or session state.
type Worker func(context.Context, string, parsers.Loot) (string, error)

func WithWorker(worker Worker) Option { return func(c *Command) { c.worker = worker } }

func WithProxy(proxy string) Option {
	return func(c *Command) { c.Proxy = proxy }
}

func WithEvents(events aop.EventPublisher) Option {
	return func(c *Command) { c.Events = events }
}

func WithLogger(logger telemetry.Logger) Option {
	return func(c *Command) { c.InitLogger(logger) }
}

func WithDeepBrowserFunc(fn func(context.Context, string) (string, error)) Option {
	return func(c *Command) { c.deepBrowser = fn }
}

// WithExecutionOnly rejects modes requiring inference rather than silently ignoring them.
func WithExecutionOnly() Option { return func(c *Command) { c.executionOnly = true } }
