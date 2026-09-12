package runtime

import (
	"context"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/output"
	"testing"
)

func loadOutputRecorder(t *testing.T, recorder *output.JSONLRecorder) error {
	t.Helper()
	set, err := extension.New(extension.Entry{ID: "recorder", Extension: recorder})
	if err != nil {
		return err
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	return set.Load(t.Context())
}
