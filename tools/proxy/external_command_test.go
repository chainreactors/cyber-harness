package proxy

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	coretool "github.com/chainreactors/cyber/core/tool"
)

func TestProxyPassthroughExternalProgram(t *testing.T) {
	resource := NewProxyHub(NewState(""), nil, t.TempDir(), false, nil)
	if err := resource.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	command := New(resource.ProxyHub.State())
	command.SetHub(resource.ProxyHub)
	command.SetCommandExecutor(func(ctx context.Context, argv []string, parent *coretool.Execution) (any, error) {
		return coretool.RunCommand(ctx, nil, argv, parent)
	})
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	_, err = command.Run(t.Context(), &coretool.Execution{
		Args: []string{"http://127.0.0.1:1", program, "-test.run=^TestProxyExternalChild$"},
		Env:  []string{"PROXY_EXTERNAL_CHILD=1"}, Stdout: &output, Stderr: &output,
	})
	if err != nil || !strings.Contains(output.String(), "@127.0.0.1:") {
		t.Fatalf("external proxy route=%q err=%v", output.String(), err)
	}
}

func TestProxyExternalChild(t *testing.T) {
	if os.Getenv("PROXY_EXTERNAL_CHILD") == "1" {
		_, _ = os.Stdout.WriteString(os.Getenv("HTTP_PROXY"))
	}
}
