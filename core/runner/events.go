package runner

import (
	"os"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
)

type eventsFileSubscriber struct {
	w *aop.Writer
	f *os.File
}

func newEventsFileSubscriber(path string) (*eventsFileSubscriber, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &eventsFileSubscriber{
		w: aop.NewWriter(f, "aiscan"),
		f: f,
	}, nil
}

func (s *eventsFileSubscriber) Close() {
	_ = s.f.Close()
}

func (s *eventsFileSubscriber) HandleEvent(event agent.Event) {
	s.w.HandleEvent(event)
}
