package session

import (
	"context"
	"testing"

	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	telemetry "github.com/chainreactors/cyber/pkg/exts/telemetry"
)

// loadtelemetry mounts a recorder against the stream it should observe. The
// recorder borrows that stream, so the host has to publish it.
func loadtelemetry(t *testing.T, stream *coreevents.Stream, output *telemetry.Extension) error {
	t.Helper()
	set, err := extension.New(extension.Provided[*coreevents.Stream](stream), output)
	if err != nil {
		return err
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return set.Load(t.Context())
}
