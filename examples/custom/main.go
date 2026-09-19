// Command custom demonstrates the smallest useful Cyber composition root.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/base"
)

func main() {
	entries, err := base.New(base.Config{
		Directory: ".",
		Provider:  provider.StartupConfig{Mode: provider.StartupDisabled},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	entries = append(entries, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		_, err := extension.Use[*app.State](scope)
		return err
	}})
	set, err := extension.New(entries...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := set.Load(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := set.Close(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
