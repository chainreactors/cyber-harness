package console

import (
	"context"
	"github.com/chainreactors/aiscan/core/extension"
	eventoutput "github.com/chainreactors/aiscan/pkg/exts/eventoutput"
	"testing"
)

func loadOutputRecorder(t *testing.T, recorder *eventoutput.Extension) error {
	t.Helper()
	set, err := extension.New(extension.Entry{ID: "recorder", Extension: recorder})
	if err != nil {
		return err
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return set.Load(t.Context())
}
