package console

import (
	"context"
	"github.com/chainreactors/cyber/core/extension"
	telemetry "github.com/chainreactors/cyber/pkg/exts/telemetry"
	"testing"
)

func loadOutputRecorder(t *testing.T, recorder *telemetry.Extension) error {
	t.Helper()
	set, err := extension.New(extension.Entry{ID: "recorder", Extension: recorder})
	if err != nil {
		return err
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return set.Load(t.Context())
}

