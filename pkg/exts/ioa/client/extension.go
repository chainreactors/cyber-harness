// Package client installs the complete IOA client collaboration capability.
package client

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/skills"
	service "github.com/chainreactors/cyber/tools/ioa"
)

// DeliverFunc may reject calls outside its receiver's lifetime. It does not
// lend ownership of the receiver to IOA.
type DeliverFunc func(context.Context, inbox.Message) error

type Dependencies struct {
	Events  *events.Stream
	Deliver DeliverFunc
	Logger  telemetry.Logger
	Skills  []skills.Bundle
}

type Extension struct {
	resource      *service.Resource
	config        service.Config
	deps          Dependencies
	sub           *eventbus.Subscription[*aop.Event]
	receiveCancel context.CancelFunc
	sendCancel    context.CancelFunc
	workers       sync.WaitGroup
	done          chan struct{}
	outputMu      sync.Mutex
	outputErr     error
}

func New(config service.Config, deps Dependencies) *Extension {
	if deps.Logger == nil {
		deps.Logger = telemetry.NopLogger()
	}
	return &Extension{resource: service.New(config, deps.Logger), config: config, deps: deps}
}

func (e *Extension) Service() *service.Service {
	if e == nil || e.resource == nil {
		return nil
	}
	return e.resource.Service
}

func (e *Extension) Load(scope *extension.Scope) error {
	if len(e.deps.Skills) > 0 {
		if err := extension.Add(scope, e.deps.Skills...); err != nil {
			return err
		}
	}
	if err := e.resource.Start(scope.Init()); err != nil {
		return err
	}
	if e.config.RegisterCommands {
		if values := e.resource.Commands(); len(values) > 0 {
			if err := extension.Add(scope, values...); err != nil {
				return err
			}
		}
	}
	rt := e.Service()
	client := e.resource.Client()
	if client == nil {
		return nil
	}
	receiveCtx, receiveCancel := context.WithCancel(scope.Lifetime())
	e.receiveCancel = receiveCancel
	if e.deps.Events != nil && e.config.Space != "" {
		// Output survives Scope cancellation to drain Agent termination events.
		// Close owns this context and joins the consumer before releasing resources.
		sendCtx, cancel := context.WithCancel(context.Background())
		e.sendCancel = cancel
		var err error
		e.sub, err = consumeHandoff(e.deps.Events, newHandoff(sendCtx, client, e.config.Space, e.deps.Logger, func(err error) {
			e.outputMu.Lock()
			// This records an output failure, not incomplete resource cleanup.
			// Do not unwrap a canceled send into Set's Close retry policy.
			e.outputErr = fmt.Errorf("IOA handoff failed: %v", err)
			e.outputMu.Unlock()
			rt.ReportError(err)
		}))
		if err != nil {
			return err
		}
		e.workers.Add(1)
		telemetry.SafeGo("ioa-output-status", func() {
			defer e.workers.Done()
			select {
			case <-receiveCtx.Done():
				return
			case <-e.sub.Stopped():
				rt.ReportDropped(e.sub.Dropped())
				rt.ReportError(e.sub.Err())
			}
		})
	}
	if e.deps.Deliver != nil && e.config.Space != "" {
		e.workers.Add(1)
		telemetry.SafeGo("ioa-inbox", func() {
			defer e.workers.Done()
			if err := rt.WaitReady(receiveCtx); err != nil {
				return
			}
			subscribeIOASpace(receiveCtx, client, rt.ReceiveSpace(), client.NodeID, func(message inbox.Message) error {
				err := e.deps.Deliver(receiveCtx, message)
				if err != nil && receiveCtx.Err() == nil {
					rt.ReportError(err)
				}
				return err
			}, e.deps.Logger, rt.ReportError)
		})
	}
	e.done = make(chan struct{})
	go func() { e.workers.Wait(); close(e.done) }()
	return nil
}

func (e *Extension) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if e.receiveCancel != nil {
		e.receiveCancel()
	}
	if e.done != nil {
		select {
		case <-e.done:
		default:
			select {
			case <-e.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	var outputErr error
	if e.sub != nil {
		if err := e.sub.Close(ctx); err != nil {
			if e.sendCancel != nil {
				e.sendCancel()
			}
			return err
		}
		e.Service().ReportDropped(e.sub.Dropped())
		outputErr = e.sub.Err()
		if dropped := e.sub.Dropped(); dropped > 0 {
			outputErr = errors.Join(outputErr, fmt.Errorf("IOA output incomplete: %d events dropped", dropped))
		}
	}
	if e.sendCancel != nil {
		e.sendCancel()
	}
	if err := e.resource.Close(ctx); err != nil {
		return err
	}
	e.outputMu.Lock()
	defer e.outputMu.Unlock()
	return errors.Join(outputErr, e.outputErr)
}

var _ extension.Extension = (*Extension)(nil)
