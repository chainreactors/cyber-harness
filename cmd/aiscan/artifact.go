package main

import (
	"context"
	"errors"
	"fmt"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/eventbus"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	"google.golang.org/protobuf/proto"
)

const (
	artifactQueue = 256
	artifactBytes = 16 << 20
)

// artifactProjection is the Profile-owned asynchronous consumer of canonical
// AOP artifact events. It owns queue admission and drain; neither App nor the
// Web server contains callback wiring for artifact observations.
type artifactProjection struct {
	events    *coreevents.Stream
	artifacts coretool.ArtifactImporter
	logger    telemetry.Logger
	work      context.Context
	cancel    context.CancelFunc
	sub       *eventbus.Subscription[*aop.Event]
}

func newArtifactProjection(events *coreevents.Stream, artifacts coretool.ArtifactImporter, logger telemetry.Logger) (*artifactProjection, error) {
	if events == nil || artifacts == nil {
		return nil, fmt.Errorf("artifact projection requires an event stream and importer")
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	return &artifactProjection{events: events, artifacts: artifacts, logger: logger}, nil
}

func (p *artifactProjection) Load(scope *extension.Scope) error {
	if p == nil || scope == nil {
		return fmt.Errorf("artifact projection is required")
	}
	if p.sub != nil {
		return nil
	}
	// Set.Close drains this consumer before canceling its work. The scope still
	// contributes values, but its early cancellation cannot discard admitted
	// observations during shutdown.
	p.work, p.cancel = context.WithCancel(context.WithoutCancel(scope.Lifetime()))
	sub, err := p.events.Consume(eventbus.SubscribeOptions[*aop.Event]{
		Buffer:   artifactQueue,
		MaxBytes: artifactBytes,
		Filter: func(event *aop.Event) bool {
			extension := event.GetExtension()
			return extension != nil && extension.MessageIs(new(toolpb.Artifact))
		},
		Size: func(event *aop.Event) int64 { return int64(proto.Size(event)) },
		Clone: func(event *aop.Event) *aop.Event {
			if event == nil {
				return nil
			}
			return proto.Clone(event).(*aop.Event)
		},
	}, p)
	if err != nil {
		p.cancel()
		p.work, p.cancel = nil, nil
		return err
	}
	p.sub = sub
	return nil
}

func (p *artifactProjection) ConsumeEvent(event *aop.Event) error {
	artifact, operationID, found, err := toolpb.FromEvent(event)
	if err != nil {
		p.logger.Warnf("artifact observation incomplete: %v", err)
		return err
	}
	if !found {
		return nil
	}
	if _, _, err := p.artifacts.ImportArtifact(p.work, operationID, artifact); err != nil {
		if !errors.Is(err, context.Canceled) {
			p.logger.Warnf("artifact projection incomplete: %v", err)
		}
		return err
	}
	return nil
}

func (p *artifactProjection) Flush(ctx context.Context) error {
	if p == nil || p.sub == nil {
		return nil
	}
	if err := p.sub.Flush(ctx); err != nil {
		return err
	}
	return p.status()
}

func (p *artifactProjection) Close(ctx context.Context) error {
	if p == nil || p.sub == nil {
		return nil
	}
	if err := p.sub.Close(ctx); err != nil {
		return err
	}
	if p.cancel != nil {
		p.cancel()
	}
	return p.status()
}

func (p *artifactProjection) status() error {
	err := p.sub.Err()
	if dropped := p.sub.Dropped(); dropped > 0 {
		err = errors.Join(err, fmt.Errorf("artifact projection incomplete: %d events dropped", dropped))
	}
	return err
}

var _ extension.Extension = (*artifactProjection)(nil)
var _ coreevents.Consumer = (*artifactProjection)(nil)
