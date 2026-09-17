package console

import (
	"context"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	telemetry "github.com/chainreactors/cyber/pkg/exts/telemetry"
	"github.com/chainreactors/cyber/pkg/hosttest"
	"testing"
)

// loadOutputRecorder mounts a recorder against the stream it should observe.
func loadOutputRecorder(t *testing.T, stream *coreevents.Stream, recorder *telemetry.Extension) error {
	t.Helper()
	set, err := extension.New(hosttest.Provide[*coreevents.Stream](stream), recorder)
	if err != nil {
		return err
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return set.Load(t.Context())
}
