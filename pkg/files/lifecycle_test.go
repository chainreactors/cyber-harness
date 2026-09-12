package files

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
)

func filesystemSet(t *testing.T, f *FS) *extension.Set {
	t.Helper()
	s, err := extension.New(extension.Entry{ID: "filesystem", Extension: f})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}
