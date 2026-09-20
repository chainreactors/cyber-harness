// Package client installs IOA collaboration without making it a core dependency.
package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	service "github.com/chainreactors/cyber/tools/ioa"
)

type CollaborationOptions struct {
	Skills []skills.Bundle
}

type CollaborationExtension struct {
	service       *service.Service
	options       CollaborationOptions
	subscriptions []*hooks.Subscription
	ctx           context.Context
	cancel        context.CancelFunc
	workers       sync.WaitGroup
	receiveMu     sync.Mutex
	receiveSpace  string
	receiveCancel context.CancelFunc
	mu            sync.Mutex
	routes        map[string]*sessionRoute
	seen          map[string]bool
	recent        []string
	outputErr     error
}

func NewCollaboration(options CollaborationOptions) *CollaborationExtension {
	options.Skills = append([]skills.Bundle(nil), options.Skills...)
	return &CollaborationExtension{options: options, routes: make(map[string]*sessionRoute), seen: make(map[string]bool)}
}
func (e *CollaborationExtension) Service() *service.Service { return e.service }

func (e *CollaborationExtension) Load(scope *extension.Scope) error {
	svc, err := extension.Use[*service.Service](scope)
	if err != nil {
		return err
	}
	e.service = svc
	registry, err := extension.Use[*hooks.Registry](scope)
	if err != nil {
		return err
	}
	if err := extension.Add(scope, prompt.Contribution{
		Name: "ioa.collaboration.prompt", Targets: []prompt.Target{prompt.MainSystem},
		Apply: func(_ context.Context, document *prompt.Document, _ prompt.Context) error {
			return document.After(prompt.SectionIdentity, "ioa.collaboration", prompt.Static("Use ioa send --target-session for ongoing agent communication; ioa read for prior context."))
		},
	}); err != nil {
		return err
	}
	if len(e.options.Skills) > 0 {
		if err := extension.Add(scope, e.options.Skills...); err != nil {
			return err
		}
	}
	e.ctx, e.cancel = context.WithCancel(scope.Lifetime())
	e.service.SetSpaceChangeHandler(e.switchSpace)
	e.subscriptions = []*hooks.Subscription{
		agenthooks.SessionStart.On(registry, "ioa", e.sessionStart),
		agenthooks.SessionEnd.On(registry, "ioa", e.sessionEnd),
		agenthooks.BeforeRun.On(registry, "ioa", func(_ context.Context, ev agenthooks.RunStartEvent) (agenthooks.RunStartResult, error) {
			identity := fmt.Sprintf("\nIOA node=%s session=%s space=%s", e.service.Client().NodeID(), ev.SessionID, e.Service().ReceiveSpace())
			e.mu.Lock()
			if route := e.routes[ev.SessionID]; route != nil && route.start.ParentID != "" {
				identity += " parent_session=" + route.start.ParentID
			}
			e.mu.Unlock()
			system := ev.SystemPrompt + identity
			return agenthooks.RunStartResult{SystemPrompt: &system}, nil
		}),
	}
	e.workers.Add(1)
	go func() {
		defer e.workers.Done()
		if err := e.Service().WaitReady(e.ctx); err != nil {
			return
		}
		for attempt := 0; e.ctx.Err() == nil; attempt++ {
			if _, err := e.service.ReadySpace(e.ctx); err == nil {
				return
			} else {
				e.Service().ReportError(err)
			}
			select {
			case <-e.ctx.Done():
				return
			case <-time.After(retryDelay(attempt)):
			}
		}
	}()
	return nil
}

func (e *CollaborationExtension) Close(ctx context.Context) error {
	// Dependent execution extensions have already joined their tasks. Drain any
	// admitted hook before closing the SDK/store, including on a close retry.
	for _, sub := range e.subscriptions {
		sub.Cancel()
	}
	for _, sub := range e.subscriptions {
		if err := sub.Close(ctx); err != nil {
			return err
		}
	}
	if e.cancel != nil {
		e.cancel()
	}
	e.receiveMu.Lock()
	if e.receiveCancel != nil {
		e.receiveCancel()
	}
	e.receiveMu.Unlock()
	done := make(chan struct{})
	go func() { e.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if e.service != nil {
		e.service.SetSpaceChangeHandler(nil)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.routes = make(map[string]*sessionRoute)
	return e.outputErr
}

func (e *CollaborationExtension) recordError(err error) error {
	if err == nil {
		return nil
	}
	e.Service().ReportError(err)
	e.mu.Lock()
	// An output error is final, not an incomplete resource cleanup.
	e.outputErr = errors.Join(e.outputErr, fmt.Errorf("IOA record failed: %v", err))
	e.mu.Unlock()
	return err
}
