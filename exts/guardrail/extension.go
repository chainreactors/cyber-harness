// Package guardrail implements tool admission as an optional extension over
// the native JEV API. It has no dependency on Reflex declaration or execution.
package guardrail

import (
	"context"
	"errors"
	"fmt"
	"time"

	jevapi "github.com/chainreactors/cyber/agent/provider/jev"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/resource"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

const ConfigKey = "guardrail"

type Config struct {
	Rules
	Provider      string `config:"provider" json:"provider" description:"none (default) or jev"`
	Mode          Mode   `config:"mode" json:"mode" description:"auto asks the policy provider to assess consequences (default); safe asks a human; screening always applies"`
	ReviewTimeout string `config:"review_timeout" json:"review_timeout" description:"Maximum wait for tool approval (default 5m)"`
}

func (c Config) timeout() (time.Duration, error) {
	if c.Provider != "" && c.Provider != "none" && c.Provider != "jev" {
		return 0, fmt.Errorf("guardrail provider must be none or jev")
	}
	if err := c.validate(); err != nil {
		return 0, err
	}
	if c.Mode != "" && c.Mode != ModeSafe && c.Mode != ModeAuto {
		return 0, fmt.Errorf("guardrail mode must be safe or auto")
	}
	if c.ReviewTimeout == "" {
		return 5 * time.Minute, nil
	}
	d, err := time.ParseDuration(c.ReviewTimeout)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("guardrail review_timeout must be a positive duration")
	}
	return d, nil
}

func Declare(resources *resource.Registry) error {
	_, err := resource.Add[cfg.Section](resources, cfg.Section{Key: ConfigKey, New: func() any { return &Config{Rules: (Rules{}).defaults(), Mode: ModeAuto, ReviewTimeout: "5m"} }, Validate: func(v any) error { _, err := v.(*Config).timeout(); return err }})
	return err
}

type Extension struct {
	config  Config
	runtime *Runtime
	before  *hooks.Subscription
}

func New(config Config) *Extension { return &Extension{config: config} }

func (e *Extension) Load(scope *extension.Scope) error {
	timeout, err := e.config.timeout()
	if err != nil {
		return err
	}
	registry, err := extension.Use[*hooks.Registry](scope)
	if err != nil {
		return err
	}
	stream, err := extension.Use[*events.Stream](scope)
	if err != nil {
		return err
	}
	var check, confirm checkFunc
	if e.config.Provider == "jev" {
		client, err := extension.Use[*jevapi.Client](scope)
		if err != nil {
			return err
		}
		policy := newPolicy(e.config.Rules, client)
		check, confirm = policy.check, policy.confirm
	}
	e.runtime = newRuntime(scope.Lifetime(), stream, timeout, e.config.Mode, check, confirm)
	e.before = toolhooks.Before.On(registry, "guardrail", e.runtime.Admit)
	return extension.Provide[*Runtime](scope, e.runtime)
}

func (e *Extension) Close(ctx context.Context) error {
	// Close the runtime before unregistering the gate, canceling waiters/checks
	// while every remaining invocation still sees a closed admission boundary.
	if e.runtime != nil {
		if err := e.runtime.Close(ctx); err != nil {
			return errors.Join(extension.ErrCloseIncomplete, err)
		}
	}
	if e.before != nil {
		return e.before.Close(ctx)
	}
	return nil
}

var _ extension.Extension = (*Extension)(nil)
