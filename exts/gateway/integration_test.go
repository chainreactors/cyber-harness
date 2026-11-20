package gateway_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/namespaces"
	"github.com/chainreactors/cyber/core/resource"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/exts/gateway"
	passiveext "github.com/chainreactors/cyber/exts/passive"
	passiveconfig "github.com/chainreactors/cyber/exts/passive/config"
	proxyext "github.com/chainreactors/cyber/exts/proxy"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/harness"
	"github.com/chainreactors/cyber/tools/passive"
)

func TestDistributedConfigUsesOriginalCommandsWithoutAgent(t *testing.T) {
	var expectedKey atomic.Value
	expectedKey.Store("first-secret")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/subscription" {
			fmt.Fprint(w, "socks5://127.0.0.1:1001#office\n")
			return
		}
		requests.Add(1)
		if r.URL.Query().Get("key") != expectedKey.Load().(string) {
			t.Error("query did not use the applied credential")
		}
		query, _ := base64.StdEncoding.DecodeString(r.URL.Query().Get("qbase64"))
		if string(query) == "denied" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if string(query) == "partial" {
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `{"error":true,"errmsg":"account quota exhausted"}`)
			} else {
				fmt.Fprint(w, `{"error":false,"size":2,"results":[["192.0.2.10","443","https://example.test","example.test","Asset","ICP"]]}`)
			}
			return
		}
		if string(query) == "fail" {
			fmt.Fprint(w, `{"error":true,"errmsg":"account quota exhausted"}`)
			return
		}
		if string(query) != `domain="example.test"` {
			t.Errorf("query changed: %s", query)
		}
		fmt.Fprint(w, `{"error":false,"size":1,"results":[["192.0.2.10","443","https://example.test","example.test","Asset","ICP"]]}`)
	}))
	defer server.Close()
	sections := cfg.NewSections()
	resources := resource.New()
	if _, err := resource.Define[cfg.Section](resources, sections); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Declare(resources, passiveconfig.FofaSection(), proxyext.ClashSection()); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	host := &cfg.Option{Sections: sections, Explicit: map[string]bool{}, Context: &cfg.Context{Directory: directory, Home: t.TempDir(), LookupEnv: func(string) (string, bool) { return "", false }}}
	var catalog []string
	for _, key := range []string{"", "first-secret", "second-secret", "disabled"} {
		var distributed types.DistributeConfig
		if key != "" {
			expectedKey.Store(key)
			// The external source provides ordinary flat YAML; only the
			// existing wire boundary adds its extensions envelope.
			input := &cfg.Option{Sections: sections}
			document := fmt.Sprintf("gateway:\n  fofa:\n    key: %s\n    url: %s/search\n    proxy: ''\n  office:\n    kind: clash\n    url: %s/subscription\n    refresh: '0s'\n    strategy: first\n", key, server.URL, server.URL)
			if key == "disabled" {
				document = "gateway:\n  fofa:\n    enabled: false\n"
			}
			if err := cfg.LoadConfigBytes([]byte(document), input); err != nil {
				t.Fatal(err)
			}
			var err error
			distributed.Extensions, err = cfg.ValuesToProto(input.Extensions)
			if err != nil {
				t.Fatal(err)
			}
		}
		next, err := cfg.ResolveDistributedRuntime(&distributed, host)
		if err != nil {
			t.Fatal(err)
		}
		values, err := gateway.Read(next)
		if err != nil {
			t.Fatal(err)
		}
		proxy := proxyext.New(proxyext.Config{WorkDir: directory})
		query := passiveext.New(passive.ReconOptions{FofaKey: "legacy-secret", IngressProxy: "http://127.0.0.1:1"})
		base, err := harness.BaseExtensions(harness.BaseConfig{Directory: directory, Egress: proxy, Provider: provider.StartupConfig{Mode: provider.StartupDisabled}})
		if err != nil {
			t.Fatal(err)
		}
		var commands coretool.CommandExecutor
		entries := append([]extension.Extension{namespaces.New()}, base...)
		entries = append(entries, gateway.New(values, query.Gateway(""), proxy.Gateway("")), query, extension.Func{LoadFunc: func(scope *extension.Scope) error {
			var err error
			commands, err = extension.Use[coretool.CommandExecutor](scope)
			return err
		}})
		set, err := extension.New(entries...)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = set.Close(context.Background()) })
		if err := set.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		if key == "" {
			catalog = commands.Names()
		} else if key == "disabled" {
			var output bytes.Buffer
			_, err := commands.Execute(t.Context(), "passive", &coretool.Execution{Args: []string{"-s", "fofa", "fail"}, Stdout: &output, Stderr: &output})
			if err == nil || !strings.Contains(err.Error(), "unknown source") {
				t.Fatalf("disabled binding fell back to legacy credentials: %v", err)
			}
		} else {
			if !reflect.DeepEqual(catalog, commands.Names()) {
				t.Fatalf("gateway changed command catalog: %v -> %v", catalog, commands.Names())
			}
			var output bytes.Buffer
			_, err := commands.Execute(t.Context(), "passive", &coretool.Execution{Args: []string{"-s", "fofa", `domain="example.test"`}, Stdout: &output, Stderr: &output})
			if err != nil || !strings.Contains(output.String(), `"title":"Asset"`) || strings.Contains(output.String(), "secret") {
				t.Fatalf("original query command: %s err=%v", output.String(), err)
			}
			output.Reset()
			_, err = commands.Execute(t.Context(), "proxy", &coretool.Execution{Args: []string{"list"}, Stdout: &output, Stderr: &output})
			if err != nil || !strings.Contains(output.String(), "office") {
				t.Fatalf("subscription was not configured: %s err=%v", output.String(), err)
			}
			output.Reset()
			_, err = commands.Execute(t.Context(), "passive", &coretool.Execution{Args: []string{"-s", "fofa", "fail"}, Stdout: &output, Stderr: &output})
			if err == nil || !strings.Contains(err.Error(), "quota exhausted") {
				t.Fatalf("provider failure reported as empty data: %s err=%v", output.String(), err)
			}
			_, err = commands.Execute(t.Context(), "passive", &coretool.Execution{Args: []string{"-s", "fofa", "denied"}, Stdout: &output, Stderr: &output})
			if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), key) {
				t.Fatalf("HTTP error lost its status or exposed the credential: %v", err)
			}
			output.Reset()
			_, err = commands.Execute(t.Context(), "passive", &coretool.Execution{Args: []string{"-s", "fofa", "partial"}, Stdout: &output, Stderr: &output})
			if err == nil || !strings.Contains(err.Error(), "quota exhausted") || !strings.Contains(output.String(), `"title":"Asset"`) {
				t.Fatalf("paged failure discarded valid results: %s err=%v", output.String(), err)
			}
		}
		if commands.Has("gateway") {
			t.Fatal("gateway became a user command")
		}
		if err := set.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		host = next
	}
	if requests.Load() != 10 {
		t.Fatalf("unexpected query count: %d", requests.Load())
	}
	removed, err := cfg.ResolveDistributedRuntime(&types.DistributeConfig{}, host)
	if err != nil {
		t.Fatal(err)
	}
	values, err := gateway.Read(removed)
	if err != nil || len(values) != 0 {
		t.Fatalf("full replacement resurrected services: %v %v", values, err)
	}
}
