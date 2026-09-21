package spray_test

import (
	"testing"

	"github.com/chainreactors/cyber/tools/spray"
)

func TestSprayInjectProxy(t *testing.T) {
	proxyAddr := "socks5://127.0.0.1:1080"

	cmd := spray.New(nil).WithProxy(proxyAddr)

	injected := cmd.TestInjectProxy([]string{"-u", "http://example.com"})
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
}
