package service

import (
	"testing"

	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
)

// newTestApp builds the application a test owns. It carries only what App
// owns: a stream to publish on, provider state, progress and a logger.
func newTestApp(t testing.TB, logger telemetry.Logger, stream *events.Stream) *apppkg.App {
	t.Helper()
	if stream == nil {
		stream = events.New()
	}
	application, err := apppkg.New(logger, stream)
	if err != nil {
		t.Fatal(err)
	}
	return application
}
