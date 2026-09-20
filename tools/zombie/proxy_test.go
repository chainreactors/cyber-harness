package zombie_test

import (
	"bytes"
	"context"
	"testing"

	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/zombie"
)

func TestZombieExecuteWithProxy(t *testing.T) {
	proxyAddr := "socks5://127.0.0.1:1080"

	cmd := zombie.New(nil).WithProxy(proxyAddr)

	// Execute with --help just to verify no panic; the proxy is built
	// but not exercised because --help exits before any network I/O.
	var output bytes.Buffer
	_, err := cmd.Run(context.Background(), &coretool.Execution{Args: []string{"--help"}, Stdout: &output, Stderr: &output})
	if err != nil {
		t.Fatalf("zombie --help: %v", err)
	}
}
