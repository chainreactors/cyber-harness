package relay_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/namespaces"
	"github.com/chainreactors/cyber/core/resource"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/exts/gateway"
	proxyext "github.com/chainreactors/cyber/exts/proxy"
	relayext "github.com/chainreactors/cyber/exts/relay"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/harness"
	"github.com/chainreactors/cyber/tools/files"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const key = "cr_sub_integration"

func fakeRelay(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"code":"UNAUTHORIZED","message":"invalid key"}}`)
			return
		}
		switch r.URL.Path {
		case "/relay/v1/search":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["query"] != `domain="example.test"` || body["size"] != float64(5) {
				t.Errorf("search body: %v", body)
			}
			fmt.Fprint(w, `{"request_id":"r1","total":1,"count":1,"format":"flat","cache_hit":false,"failover_count":0,"routed_group":"asset_search",
				"assets":[{"canonical_key":"192.0.2.1:443:https","ip":"192.0.2.1","port":443,"protocol":"https","title":"Portal"}]}`)
		case "/relay/v1/search/explain":
			fmt.Fprint(w, `{"query":"ip=\"1.1.1.1\"","supported":true}`)
		case "/relay/v1/proxy/acquire":
			fmt.Fprintf(w, `{"region":"global","refreshed":false,"proxies":[{"lease_id":"5f0e8c1a-0000-4000-8000-000000000001","session_id":"task01","region":"global","protocol":"socks5","tunnel_url":"socks5://%s:global.us.sticky.task01@relay.test:18081"}]}`, key)
		default:
			t.Errorf("unexpected relay request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

type loaded struct {
	commands coretool.CommandExecutor
	store    *skills.Store
	files    *files.Files
}

func load(t *testing.T, values cfg.Values) loaded {
	t.Helper()
	directory := t.TempDir()
	proxy := proxyext.New(proxyext.Config{WorkDir: directory})
	relay := relayext.New()
	base, err := harness.BaseExtensions(harness.BaseConfig{Directory: directory, Egress: proxy, Provider: provider.StartupConfig{Mode: provider.StartupDisabled}})
	if err != nil {
		t.Fatal(err)
	}
	var out loaded
	entries := append([]extension.Extension{namespaces.New()}, base...)
	entries = append(entries, gateway.New(values, relay.Gateway()), relay, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		if out.commands, err = extension.Use[coretool.CommandExecutor](scope); err != nil {
			return err
		}
		if out.store, err = extension.Use[*skills.Store](scope); err != nil {
			return err
		}
		out.files, err = extension.Use[*files.Files](scope)
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
	return out
}

func hasSkill(store *skills.Store) bool {
	for _, skill := range store.All() {
		if skill.Name == "cyber-relay" {
			return true
		}
	}
	return false
}

func sections(t *testing.T) *cfg.Sections {
	t.Helper()
	sections := cfg.NewSections()
	resources := resource.New()
	if _, err := resource.Define[cfg.Section](resources, sections); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Define[cfg.Connection](resources, sections.ConnectionPoint()); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Declare(resources, relayext.Section()); err != nil {
		t.Fatal(err)
	}
	if err := relayext.Declare(resources); err != nil {
		t.Fatal(err)
	}
	return sections
}

func TestConfiguredRelayLoadsSkillAndCommand(t *testing.T) {
	server := fakeRelay(t)
	sections := sections(t)
	input := &cfg.Option{Sections: sections}
	document := fmt.Sprintf("gateway:\n  relay:\n    base_url: %s/relay/\n    key: ' %s '\n", server.URL, key)
	if err := cfg.LoadConfigBytes([]byte(document), input); err != nil {
		t.Fatal(err)
	}
	values, err := gateway.Read(input)
	if err != nil {
		t.Fatal(err)
	}
	got := load(t, values)
	if !hasSkill(got.store) || !got.commands.Has("relay") {
		t.Fatal("cyber-relay skill or relay command missing with gateway.relay configured")
	}
	for _, doc := range []string{"SKILL.md", "asset-search.md", "web.md", "hash.md", "captcha.md", "sms.md", "proxy.md"} {
		if body, ok, err := got.store.ReadVirtual("cyber://skills/relay/" + doc); err != nil || !ok || !strings.Contains(body, "relay ") {
			t.Errorf("skill document %s: ok=%v err=%v", doc, ok, err)
		}
		if _, err := got.files.Read(t.Context(), "cyber://skills/relay/"+doc); err != nil {
			t.Errorf("document %s was not mounted: %v", doc, err)
		}
	}
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if _, err := got.commands.Execute(t.Context(), "relay", &coretool.Execution{Args: args, Stdout: &out, Stderr: &out}); err != nil {
			t.Fatalf("relay %v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}
	if out := run("search-assets", `domain="example.test"`, "--size", "5"); !strings.Contains(out, `"title": "Portal"`) || !strings.Contains(out, `"routed_group": "asset_search"`) {
		t.Fatalf("search-assets: %s", out)
	}
	if out := run("acquire-proxy", "--region", "global", "--session-id", "task01"); !strings.Contains(out, "socks5://"+key+":global.us.sticky.task01@relay.test:18081") {
		t.Fatalf("acquire-proxy must return the usable tunnel URL: %s", out)
	}

	var distributed types.DistributeConfig
	if distributed.Extensions, err = cfg.ValuesToProto(input.Extensions); err != nil {
		t.Fatal(err)
	}
	checks, err := sections.TestConnection(t.Context(), "gateway.relay", &distributed, nil)
	if err != nil || len(checks) != 1 || !checks[0].Ok || checks[0].Detail != "authenticated" {
		t.Fatalf("connection check: %v %v", checks, err)
	}
	// A partial edit without the key reuses the stored one.
	edited := cfg.CloneValues(input.Extensions)
	delete(edited["gateway"]["relay"].(map[string]any), "key")
	partial := &types.DistributeConfig{}
	if partial.Extensions, err = cfg.ValuesToProto(edited); err != nil {
		t.Fatal(err)
	}
	if checks, err := sections.TestConnection(t.Context(), "gateway.relay", partial, &distributed); err != nil || len(checks) != 1 || !checks[0].Ok {
		t.Fatalf("partial edit connection check: %v %v", checks, err)
	}
	// A wrong key fails the check instead of passing as "no scope".
	edited["gateway"]["relay"].(map[string]any)["key"] = "cr_sub_wrong"
	if partial.Extensions, err = cfg.ValuesToProto(edited); err != nil {
		t.Fatal(err)
	}
	if checks, err := sections.TestConnection(t.Context(), "gateway.relay", partial, &distributed); err != nil || len(checks) != 1 || checks[0].Ok || !strings.Contains(checks[0].Error, "UNAUTHORIZED") {
		t.Fatalf("wrong key passed the connection check: %v %v", checks, err)
	}
}

func TestConnectionCheckGuardsSecretsAnd403Codes(t *testing.T) {
	var mu sync.Mutex
	var status int
	var body string
	var seenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenAuth = r.Header.Get("Authorization")
		st, bd := status, body
		mu.Unlock()
		w.WriteHeader(st)
		fmt.Fprint(w, bd)
	}))
	t.Cleanup(server.Close)
	sections := sections(t)

	// Re-typing a non-relay stored entry (implicit kind "fofa") to kind "relay"
	// with an empty key must not copy the stored key from the other service.
	stored := &types.DistributeConfig{}
	var err error
	if stored.Extensions, err = cfg.ValuesToProto(cfg.Values{
		"gateway": {
			"fofa":  map[string]any{"base_url": "https://fofa.test", "key": "fofa_secret"},
			"relay": map[string]any{"base_url": "https://old-relay.test", "key": key},
		},
	}); err != nil {
		t.Fatal(err)
	}
	incoming := &types.DistributeConfig{}
	if incoming.Extensions, err = cfg.ValuesToProto(cfg.Values{
		"gateway": {"fofa": map[string]any{"kind": "relay", "base_url": server.URL}},
	}); err != nil {
		t.Fatal(err)
	}
	checks, err := sections.TestConnection(t.Context(), "gateway.relay", incoming, stored)
	mu.Lock()
	gotAuth := seenAuth
	mu.Unlock()
	if err != nil || len(checks) != 1 || checks[0].Ok || gotAuth != "" {
		t.Fatalf("cross-kind key preservation check: checks=%v err=%v seenAuth=%q", checks, err, gotAuth)
	}

	// Changing base_url with an empty key must not send the stored relay key to
	// the new endpoint.
	changedURL := &types.DistributeConfig{}
	if changedURL.Extensions, err = cfg.ValuesToProto(cfg.Values{
		"gateway": {"relay": map[string]any{"base_url": server.URL}},
	}); err != nil {
		t.Fatal(err)
	}
	checks, err = sections.TestConnection(t.Context(), "gateway.relay", changedURL, stored)
	mu.Lock()
	gotAuth = seenAuth
	mu.Unlock()
	if err != nil || len(checks) != 1 || checks[0].Ok || gotAuth != "" {
		t.Fatalf("changed base_url leaked stored key: checks=%v err=%v seenAuth=%q", checks, err, gotAuth)
	}

	// Invalid metadata and multiple enabled relay instances must fail before probing.
	for name, gw := range map[string]map[string]any{
		"non-bool enabled": {
			"relay": map[string]any{"base_url": server.URL, "key": key, "enabled": "false"},
		},
		"invalid kind": {
			"relay": map[string]any{"base_url": server.URL, "key": key, "kind": 123},
		},
		"multiple relay instances": {
			"relay":  map[string]any{"base_url": server.URL, "key": key},
			"backup": map[string]any{"kind": "relay", "base_url": server.URL, "key": key},
		},
	} {
		t.Run(name, func(t *testing.T) {
			doc := &types.DistributeConfig{}
			if doc.Extensions, err = cfg.ValuesToProto(cfg.Values{"gateway": gw}); err != nil {
				t.Fatal(err)
			}
			checks, err := sections.TestConnection(t.Context(), "gateway.relay", doc, nil)
			if err != nil || len(checks) != 1 || checks[0].Ok {
				t.Fatalf("%s: checks=%v err=%v", name, checks, err)
			}
		})
	}

	// Only valid explain responses or SCOPE_DENIED/INSUFFICIENT_SCOPE 403 pass.
	probe := &types.DistributeConfig{}
	if probe.Extensions, err = cfg.ValuesToProto(cfg.Values{
		"gateway": {"relay": map[string]any{"base_url": server.URL, "key": key}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		status int
		body   string
		wantOK bool
	}{
		{"valid explain", http.StatusOK, `{"query":"ip=\"1.1.1.1\"","supported":false}`, true},
		{"generic empty json", http.StatusOK, `{}`, false},
		{"scope denied", http.StatusForbidden, `{"error":{"code":"SCOPE_DENIED","message":"no scope"}}`, true},
		{"insufficient scope", http.StatusForbidden, `{"error":{"code":"INSUFFICIENT_SCOPE","message":"no scope"}}`, true},
		{"ip not allowed", http.StatusForbidden, `{"error":{"code":"IP_NOT_ALLOWED","message":"blocked"}}`, false},
		{"non-relay 403", http.StatusForbidden, `<html>403 Forbidden</html>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			status, body = tc.status, tc.body
			mu.Unlock()
			checks, err := sections.TestConnection(t.Context(), "gateway.relay", probe, nil)
			if err != nil || len(checks) != 1 || checks[0].Ok != tc.wantOK {
				t.Fatalf("%s: checks=%v err=%v", tc.name, checks, err)
			}
		})
	}
}

// Without an enabled gateway.relay the agent sees nothing about the relay: no
// skill, no mounted document and no command.
func TestRelayRequiresGatewayConfiguration(t *testing.T) {
	for name, values := range map[string]cfg.Values{
		"no gateway":    nil,
		"empty gateway": {},
		"disabled":      {"relay": {"enabled": false}},
	} {
		t.Run(name, func(t *testing.T) {
			got := load(t, values)
			if hasSkill(got.store) {
				t.Fatal("cyber-relay skill registered without gateway.relay")
			}
			if _, ok, _ := got.store.ReadVirtual("cyber://skills/relay/SKILL.md"); ok {
				t.Fatal("cyber-relay skill readable without gateway.relay")
			}
			if _, err := got.files.Read(t.Context(), "cyber://skills/relay/SKILL.md"); err == nil {
				t.Fatal("cyber-relay document mounted without gateway.relay")
			}
			if got.commands.Has("relay") {
				t.Fatal("relay command registered without gateway.relay")
			}
		})
	}
}

func TestSectionValidation(t *testing.T) {
	for _, fields := range []map[string]any{
		{"key": "k"},
		{"base_url": "https://relay.test"},
		{"base_url": "https://relay.test", "key": "   "},
		{"base_url": "https://relay.test?x=1", "key": "k"},
		{"base_url": "https://relay.test", "key": "k", "proxy": "cn"},
	} {
		if _, err := relayext.Section().Decode(fields); err == nil {
			t.Errorf("accepted %v", fields)
		}
	}
}
