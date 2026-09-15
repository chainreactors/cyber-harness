// Package signals groups the shared Hook and AOP event channels. Capability
// extensions borrow these interfaces; the profile owns this one installation.
package signals

import (
	"context"
	"fmt"

	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
)

type Extension struct {
	hooks  *hooks.Registry
	events *events.Stream
}

func (e *Extension) Descriptor() extension.Descriptor {
	return extension.Descriptor{ID: "signals", Description: "hook and AOP event channels"}
}

func New() *Extension { return &Extension{hooks: hooks.New(), events: events.New()} }
func (e *Extension) Hooks() *hooks.Registry {
	if e == nil {
		return nil
	}
	return e.hooks
}
func (e *Extension) Events() *events.Stream {
	if e == nil {
		return nil
	}
	return e.events
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.hooks == nil || e.events == nil || scope == nil {
		return fmt.Errorf("signals is unavailable")
	}
	return scope.Init().Err()
}

// Hook/event subscriptions are owned by their contributors. Closing the
// channel extension therefore only prevents new profile use; it does not
// guess which subscriptions belong to unrelated capabilities.
func (e *Extension) Close(context.Context) error { return nil }

var _ extension.Extension = (*Extension)(nil)
