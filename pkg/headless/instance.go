//go:build browser

// Ported from nuclei pkg/protocols/headless/engine/instance.go.
// Provides isolated incognito browser context per template execution.

package headless

import (
	"context"
	"errors"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/utils"
)

// Instance is an isolated incognito browser context for a single template execution.
// Each Instance has its own cookie jar, storage, and page state — preventing
// cross-contamination between concurrent or sequential template runs.
type Instance struct {
	engine     *Engine
	browser    *rod.Browser // incognito context
	requestLog map[string]string
}

// NewInstance creates a new incognito browser instance from the engine.
func (e *Engine) NewInstance() (*Instance, error) {
	incognito, err := e.browser.Incognito()
	if err != nil {
		return nil, err
	}
	incognito = incognito.Sleeper(func() utils.Sleeper { return maxBackoffSleeper(10) })
	return &Instance{
		engine:     e,
		browser:    incognito,
		requestLog: make(map[string]string),
	}, nil
}

// GetRequestLog returns a map of [template-defined-URL] → [actual-request-URL].
func (i *Instance) GetRequestLog() map[string]string {
	return i.requestLog
}

// Close closes all tabs/pages in this incognito context.
func (i *Instance) Close() error {
	if i.browser != nil {
		return i.browser.Close()
	}
	return nil
}

// maxBackoffSleeper is a backoff sleeper with retry limit.
// Ported from nuclei: 100ms→500ms backoff, max 10 retries.
func maxBackoffSleeper(max int) utils.Sleeper {
	count := 0
	backoffSleeper := utils.BackoffSleeper(100*time.Millisecond, 500*time.Millisecond, nil)
	return func(ctx context.Context) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if count == max {
			return errors.New("max sleep count")
		}
		count++
		return backoffSleeper(ctx)
	}
}
