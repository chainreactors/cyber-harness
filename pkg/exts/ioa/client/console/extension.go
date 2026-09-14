package console

import (
	"context"
	"fmt"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/pkg/console/api"
	"github.com/chainreactors/aiscan/tools/ioa"
)

// Extension installs IOA presentation only when a profile selects a TUI.
type Extension struct {
	target   api.Registrar
	bindings *api.Bindings
}

func New(target api.Registrar, reader ioa.Reader, space, endpoint string) (*Extension, error) {
	if target == nil || reader == nil {
		return nil, fmt.Errorf("IOA presentation requires TUI registrar and IOA reader")
	}
	return &Extension{target: target, bindings: Bind(reader, space, endpoint)}, nil
}
func (e *Extension) Load(scope *extension.Scope) error {
	if err := scope.Init().Err(); err != nil {
		return err
	}
	return e.target.Register(api.Contribution{Source: "ioa.client", Bindings: e.bindings})
}
func (*Extension) Close(context.Context) error { return nil }

var _ extension.Extension = (*Extension)(nil)
