package session

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	telemetry "github.com/chainreactors/cyber/pkg/exts/telemetry"
)

func loadtelemetry(t *testing.T, output *telemetry.Extension) error {
	t.Helper()
	set, err := extension.New(output)
	if err != nil {
		return err
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return set.Load(t.Context())
}
