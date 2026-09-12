package files

import (
	"context"
	"errors"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
)

func filesystemSet(t *testing.T, f *Files) *extension.Set {
	t.Helper()
	s, err := extension.New(extension.Entry{ID: "filesystem", Extension: fileLifecycle{f}})
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

type fileLifecycle struct{ *Files }

func (f fileLifecycle) Load(c *extension.Context) error { return f.Open(c.Init()) }

func (f fileLifecycle) Close(ctx context.Context) error {
	err := f.Files.Close(ctx)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(extension.ErrCloseIncomplete, err)
	}
	return err
}
