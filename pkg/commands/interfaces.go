package commands

import (
	"context"

	"github.com/chainreactors/cyber/pkg/types"
)

type Catalog interface {
	Get(string) (*types.CommandSpec, bool)
	Has(string) bool
	All() []*types.CommandSpec
	Names() []string
	DescriptionPath(string) string
	UsageDocs() string
}
type Executor interface {
	Catalog
	Execute(context.Context, string, *Execution) (any, error)
	Run(context.Context, []string, *Execution) (any, error)
}
