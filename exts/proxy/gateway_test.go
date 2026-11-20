package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/namespaces"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/exts/gateway"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestGatewayFetchFailureRollsBackProxy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	proxy := New(Config{WorkDir: t.TempDir()})
	commands := coretool.NewCommandRegistry()
	set, err := extension.New(namespaces.New(), extension.Provided[*hooks.Registry](hooks.New()), commands, proxy,
		gateway.New(cfg.Values{"clash": {"url": server.URL + "/token-secret"}}, proxy.Gateway("")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	err = set.Load(t.Context())
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "token-secret") || strings.Contains(err.Error(), "panic") {
		t.Fatalf("unsafe subscription rollback: %v", err)
	}
	if commands.Has("proxy") {
		t.Fatal("failed composition exposed its command")
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatalf("rollback retained invalid resources: %v", err)
	}
}
