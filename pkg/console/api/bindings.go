// Package api defines inert presentation contributions without a terminal runtime.
package api

import (
	"context"
	"github.com/spf13/cobra"
	"io"
)

type Row struct{ Name, Value string }
type View struct {
	Out, Err io.Writer
	Table    func(string, [][]string)
}
type Bindings struct {
	Commands func(View) []*cobra.Command
	Complete func(context.Context, string) []string
	Status   func() []Row
}
