// Command custom demonstrates the smallest useful Cyber composition root.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/base"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// helloTool is ordinary business code. Its extension owns its registration.
type helloTool struct{}

type helloArgs struct {
	Name string `json:"name" jsonschema:"description=Name to greet"`
}

func (helloTool) Name() string        { return "hello" }
func (helloTool) Description() string { return "Return a greeting without external effects." }
func (t helloTool) Definition() *tool.Definition {
	return tool.Def(t.Name(), t.Description(), helloArgs{})
}
func (helloTool) Execute(_ context.Context, arguments string) (*tool.Result, error) {
	args, err := tool.ParseArgs[helloArgs](arguments)
	if err != nil {
		return nil, err
	}
	return tool.TextResult("Hello, " + args.Name + "!"), nil
}

func run() (resultErr error) {
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	entries, err := base.New(base.Config{
		Directory: directory,
		Provider:  provider.StartupConfig{Mode: provider.StartupDisabled},
	})
	if err != nil {
		return err
	}
	entries = append(entries, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		return extension.Add[tool.Tool](scope, helloTool{})
	}})
	var executor tool.Executor
	entries = append(entries, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		executor, err = extension.Use[tool.Executor](scope)
		return err
	}})
	set, err := extension.New(entries...)
	if err != nil {
		return err
	}
	// Close also handles partial loading. Report cleanup errors to the caller.
	defer func() { resultErr = errors.Join(resultErr, set.Close(context.Background())) }()
	if err := set.Load(context.Background()); err != nil {
		return err
	}
	result, err := executor.ExecuteTool(context.Background(), "hello", `{"name":"Cyber"}`)
	if err != nil {
		return err
	}
	fmt.Println(tool.ResultText(result))
	return nil
}
