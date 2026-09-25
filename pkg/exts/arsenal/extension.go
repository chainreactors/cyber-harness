// Package arsenal registers the package manager in an extension scope.
package arsenal

import (
	crtm "github.com/chainreactors/crtm/pkg"
	"github.com/chainreactors/cyber/core/extension"
	tool "github.com/chainreactors/cyber/tools/arsenal"
)

type Extension struct{ manager *crtm.Manager }

func New(manager *crtm.Manager) *Extension { return &Extension{manager: manager} }

func (e *Extension) Load(scope *extension.Scope) error {
	if err := e.manager.Prepare(scope.Init()); err != nil {
		return err
	}
	return extension.Add(scope, tool.NewCommand(e.manager))
}
