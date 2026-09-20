package gogo_test

import (
	"bytes"
	"context"
	"testing"

	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/gogo"
)

func TestGogoInjectProxy(t *testing.T) {
	proxyAddr := "socks5://127.0.0.1:1080"

	cmd := gogo.New(nil).WithProxy(proxyAddr)

	var output bytes.Buffer
	_, err := cmd.Run(context.Background(), &coretool.Execution{Args: []string{"--help"}, Stdout: &output, Stderr: &output})
	if err != nil {
		t.Fatalf("gogo --help with proxy: %v", err)
	}
	if output.String() == "" {
		t.Fatal("expected help output")
	}

	injected := cmd.TestInjectProxy([]string{"-i", "127.0.0.1"})
	hasProxy := false
	for i, arg := range injected {
		if arg == "--proxy" && i+1 < len(injected) && injected[i+1] == proxyAddr {
			hasProxy = true
			break
		}
	}
	if !hasProxy {
		t.Fatalf("expected --proxy %s in args, got %v", proxyAddr, injected)
	}

	alreadyHas := cmd.TestInjectProxy([]string{"-i", "127.0.0.1", "--proxy", "socks5://other:1080"})
	proxyCount := 0
	for _, arg := range alreadyHas {
		if arg == "--proxy" {
			proxyCount++
		}
	}
	if proxyCount != 1 {
		t.Fatalf("expected 1 --proxy flag (user-provided), got %d in %v", proxyCount, alreadyHas)
	}
}
