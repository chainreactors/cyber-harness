package runner

import (
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/core/output"
)

type eventsFileSubscriber struct {
	w *output.TimelineWriter
}

func newEventsFileSubscriber(path string) (*eventsFileSubscriber, error) {
	tw, err := output.NewTimelineWriter(path)
	if err != nil {
		return nil, err
	}
	return &eventsFileSubscriber{w: tw}, nil
}

func (s *eventsFileSubscriber) Close() {
	_ = s.w.Close()
}

func (s *eventsFileSubscriber) HandleEvent(event agent.Event) {
	s.w.WriteRecord(output.NewRecord(output.TypeAgent, event))
}
