package runner

import (
	"io"
	"os"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
)

type eventsFileSubscriber struct {
	w      *aop.Writer
	closer io.Closer
}

func newEventsFileSubscriber(path string) (*eventsFileSubscriber, error) {
	return newEventsSubscriber(path, os.Stdout)
}

// newEventsSubscriber follows the standard Unix stream convention: path "-"
// writes AOP JSONL to stdout without taking ownership of the stream. Any other
// value is an append-only event log path owned by the subscriber.
func newEventsSubscriber(path string, stdout io.Writer) (*eventsFileSubscriber, error) {
	if path == "-" {
		return &eventsFileSubscriber{w: aop.NewWriter(stdout, "aiscan")}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &eventsFileSubscriber{
		w:      aop.NewWriter(f, "aiscan"),
		closer: f,
	}, nil
}

func (s *eventsFileSubscriber) Close() {
	if s.closer != nil {
		_ = s.closer.Close()
	}
}

func (s *eventsFileSubscriber) Err() error { return s.w.Err() }

func (s *eventsFileSubscriber) HandleEvent(event agent.Event) {
	s.w.HandleEvent(event)
}
