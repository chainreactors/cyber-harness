package commands

import (
	"context"

	"github.com/chainreactors/cyber/core/types"
)

type Executor interface {
	Get(string) (*types.CommandSpec, bool)
	Has(string) bool
	All() []*types.CommandSpec
	Names() []string
	DescriptionPath(string) string
	UsageDocs() string
	Execute(context.Context, string, *Execution) (any, error)
	Run(context.Context, []string, *Execution) (any, error)
}
