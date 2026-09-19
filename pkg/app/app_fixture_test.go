package app

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
)

// The harness imports this package, so these tests build their own host instead
// of importing the harness.
func testSet(t testing.TB, entries ...extension.Extension) *extension.Set {
	t.Helper()
	set, err := extension.New(entries...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return set
}

// newTestApp builds the shared state a test owns. It carries only what State
// owns: a stream to publish on, provider state, progress and a logger.
func newTestApp(t testing.TB, logger telemetry.Logger, stream *events.Stream) *State {
	t.Helper()
	if stream == nil {
		stream = events.New()
	}
	application, err := New(logger, stream)
	if err != nil {
		t.Fatal(err)
	}
	return application
}
