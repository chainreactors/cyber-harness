package files

import (
	"context"
	"errors"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
)

func filesystemSet(t *testing.T, f *Resource) *extension.Set {
	t.Helper()
	s, err := extension.New(fileLifecycle{f})
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

type fileLifecycle struct{ *Resource }

func (f fileLifecycle) Load(scope *extension.Scope) error { return f.Open(scope.Init()) }

func (f fileLifecycle) Close(ctx context.Context) error {
	err := f.Resource.Close(ctx)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(extension.ErrCloseIncomplete, err)
	}
	return err
}
