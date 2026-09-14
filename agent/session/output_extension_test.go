package session

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
	telemetry "github.com/chainreactors/aiscan/pkg/exts/telemetry"
)

func loadtelemetry(t *testing.T, output *telemetry.Extension) error {
	t.Helper()
	set, err := extension.New(extension.Entry{ID: "output", Extension: output})
	if err != nil {
		return err
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return set.Load(t.Context())
}

