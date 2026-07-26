# Extending aiscan with a tool

Use a native tool when the model needs one structured capability. A tool only
implements `core/tool.Tool`; it does not need a plugin manager, lifecycle
container, global hook, or factory.

## Minimal implementation

Create `tools/echo/echo.go`:

```go
package echo

import (
	"context"

	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
)

type Args struct {
	Text string `json:"text" jsonschema:"description=Text to return"`
}

type Tool struct{}

func (Tool) Name() string        { return "echo" }
func (Tool) Description() string { return "Return text unchanged." }
func (Tool) Definition() tool.Definition {
	return tool.Def("echo", "Return text unchanged.", Args{})
}

func (Tool) Execute(ctx context.Context, arguments string) (tool.Result, error) {
	args, err := tool.ParseArgs[Args](arguments)
	if err != nil {
		return tool.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	return tool.TextResult(args.Text), nil
}

func Register(reg *commands.CommandRegistry) {
	reg.RegisterTool(Tool{})
}
```

Call `echo.Register(reg)` from the application composition point that should
expose the tool. Keep registration explicit when the tool has no Runtime
dependencies.

## Minimal test

```go
package echo

import (
	"context"
	"testing"

	"github.com/chainreactors/aiscan/pkg/commands"
)

func TestEcho(t *testing.T) {
	reg := commands.NewRegistry()
	Register(reg)

	result, err := reg.ExecuteTool(context.Background(), "echo", `{"text":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text() != "hello" {
		t.Fatalf("result = %q", result.Text())
	}
}
```

This test proves schema registration, argument decoding, dispatch, and result
conversion without starting an Agent or transport.

## Result rules

- Use `tool.TextResult` for normal text.
- Use `tool.ErrorResult` for a tool-level failure the model should observe.
- Return a Go `error` when execution itself failed.
- Add `tool.ImageBlock` only when the result contains an image.
- Put machine-readable domain output in `Result.Details`; do not encode it into
  an extra transport payload.
- Honor `ctx` for cancellation and deadlines.

## When a factory is justified

Use `commands.RegisterFactory` only when construction needs shared Runtime
dependencies such as the scanner engine set, IOA client, provider, data bus, or
working directory, or when an `init` registration must be activated by several
binaries. The factory should only construct the tool and call `RegisterTool`.

Do not add a new abstraction until at least two tools need the same behavior.

Pseudo-commands exposed through the `bash` tool are documented separately in
[`docs/development.md`](../docs/development.md).
