package app

import (
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/events"
)

// App owns the event stream it publishes on; there is nothing else to check,
// because it borrows nothing. Every other part a host once handed it is now a
// capability its owner publishes.
func TestNewRequiresAnEventStream(t *testing.T) {
	application, err := New(nil, nil)
	if application != nil || err == nil || !strings.Contains(err.Error(), "event stream") {
		t.Fatalf("New without a stream = %v, %v", application, err)
	}

	stream := events.New()
	application, err = New(nil, stream)
	if err != nil {
		t.Fatal(err)
	}
	if application.Events() != stream {
		t.Error("the application publishes on a stream its host does not own")
	}
	if application.Logger() == nil || application.Progress == nil {
		t.Error("the application is missing what it owns")
	}
}
