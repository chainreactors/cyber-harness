// Package client installs the complete IOA client collaboration capability.
package client

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/chainreactors/aiscan/agent/inbox"
	"github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	service "github.com/chainreactors/aiscan/tools/ioa"
)

// DeliverFunc may reject calls outside its receiver's lifetime. It does not
// lend ownership of the receiver to IOA.
type DeliverFunc func(context.Context, inbox.Message) error

type Dependencies struct {
	Commands *commands.Registry
	Events   *events.Stream
	Deliver  DeliverFunc
	Logger   telemetry.Logger
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

func New(config service.Config, deps Dependencies) (*Extension, error) {
	if config.RegisterCommands && deps.Commands == nil {
		return nil, fmt.Errorf("IOA command registration requires a command registry")
	}
	if deps.Logger == nil {
		deps.Logger = telemetry.NopLogger()
	}
	return &Extension{resource: service.New(config, deps.Logger), config: config, deps: deps}, nil
}

func (e *Extension) Runtime() *service.Runtime {
	if e == nil || e.resource == nil {
		return nil
	}
	return e.resource.Runtime
}

func (e *Extension) Load(scope *extension.Scope) error {
	if err := e.resource.Start(scope.Init()); err != nil {
		return err
	}
	if e.config.RegisterCommands {
		if values := e.resource.Commands(); len(values) > 0 {
			if err := e.deps.Commands.Register("ioa.client", "ioa", values...); err != nil {
				return err
			}
		}
	}
	rt := e.Runtime()
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
		e.Runtime().ReportDropped(e.sub.Dropped())
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
