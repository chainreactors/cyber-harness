package console

import (
	"fmt"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/console/api"
	"github.com/chainreactors/cyber/tools/ioa"
)

// Extension installs IOA presentation only when a profile selects a TUI.
type Extension struct {
	bindings *api.Bindings
}

func New(reader ioa.Reader, space, endpoint string) (*Extension, error) {
	if reader == nil {
		return nil, fmt.Errorf("IOA presentation requires IOA reader")
	}
	return &Extension{bindings: Bind(reader, space, endpoint)}, nil
}
func (e *Extension) Load(scope *extension.Scope) error {
	if err := scope.Init().Err(); err != nil {
		return err
	}
	return extension.Add(scope, e.bindings)
}

var _ extension.Extension = (*Extension)(nil)
