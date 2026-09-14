package session

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
	eventoutput "github.com/chainreactors/aiscan/pkg/exts/eventoutput"
)

func loadEventOutput(t *testing.T, output *eventoutput.Extension) error {
	t.Helper()
	set, err := extension.New(extension.Entry{ID: "output", Extension: output})
	if err != nil {
		return err
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return set.Load(t.Context())
}
