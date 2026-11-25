package passive

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/exts/gateway"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/harness"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	"github.com/chainreactors/cyber/tools/files"
	"github.com/chainreactors/cyber/tools/passive"
	"github.com/chainreactors/cyber/tools/scan"
)

const docsPath = "cyber://skills/passive/passive.md"

type loaded struct {
	set      *extension.Set
	commands coretool.CommandExecutor
	files    *files.Files
	store    *skills.Store
}

// load composes the extension the way cyber-scan does: gateway bindings are
// applied to the owner before it loads.
func load(t *testing.T, options passive.Config, services cfg.Values) loaded {
	t.Helper()
	entries, err := harness.BaseExtensions(harness.BaseConfig{Directory: t.TempDir(), Provider: provider.StartupConfig{Mode: provider.StartupDisabled}})
	if err != nil {
		t.Fatal(err)
	}
	owner := New(options)
	entries = append(entries, gateway.New(services, owner.Gateway("")...), owner)
	var out loaded
	entries = append(entries, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		out.commands, err = extension.Use[coretool.CommandExecutor](scope)
		if err != nil {
			return err
		}
		out.files, err = extension.Use[*files.Files](scope)
		if err != nil {
			return err
		}
		out.store, err = extension.Use[*skills.Store](scope)
		return err
	}})
	out.set = hosttest.Load(t, t.Context(), entries...)
	return out
}

func (l loaded) help(t *testing.T) string {
	t.Helper()
	var output bytes.Buffer
	if _, err := l.commands.Execute(t.Context(), "passive", &coretool.Execution{Args: []string{"-h"}, Stdout: &output, Stderr: &output}); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

func TestUnconfiguredSourcesHideCommandAndDocs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		options  passive.Config
		services cfg.Values
	}{
		{name: "unconfigured"},
		{name: "fofa disabled", options: passive.Config{FofaKey: "owner-key"}, services: cfg.Values{"fofa": {"enabled": false}}},
		{name: "keyless disabled", services: cfg.Values{"shodan-idb": {"enabled": false}}},
		{name: "blank keys", options: passive.Config{FofaKey: " ", HunterAPIKey: "\t"}, services: cfg.Values{"hunter": {"key": "  "}}},
		{name: "hunter disabled", options: passive.Config{HunterAPIKey: "owner-key"}, services: cfg.Values{"hunter": {"enabled": false}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := load(t, tc.options, tc.services)
			if host.commands.Has("passive") {
				t.Fatalf("passive registered without a configured source: %v", host.commands.Names())
			}
			if strings.Contains(host.commands.UsageDocs(), "passive") {
				t.Fatalf("command docs mention passive: %s", host.commands.UsageDocs())
			}
			if _, err := host.files.Read(t.Context(), docsPath); err == nil {
				t.Fatal("passive docs mounted without a configured source")
			}
			if _, ok, _ := host.store.ReadVirtualBody(scan.ScannerConceptURI("passive")); ok {
				t.Fatal("skill store serves passive docs without a configured source")
			}
			if err := host.set.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConfiguredSourcesOnlyAreDescribed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		options  passive.Config
		services cfg.Values
		want     string
	}{
		{name: "fofa", options: passive.Config{FofaKey: "owner-key"}, want: "fofa"},
		{name: "gateway fofa", services: cfg.Values{"fofa": {"key": "gateway-key"}}, want: "fofa"},
		{name: "hunter without fofa", options: passive.Config{HunterAPIKey: "owner-key"}, want: "hunter"},
		{name: "blank gateway key keeps host credential", options: passive.Config{HunterAPIKey: "owner-key"}, services: cfg.Values{"hunter": {"key": "   "}}, want: "hunter"},
		{name: "gateway hunter without fofa", services: cfg.Values{"hunter": {"key": "gateway-key"}}, want: "hunter"},
		{name: "keyless enabled", services: cfg.Values{"shodan-idb": {"enabled": true}}, want: "shodan-idb"},
		{name: "fofa and keyless", options: passive.Config{FofaKey: "owner-key"}, services: cfg.Values{"shodan-idb": {}}, want: "fofa, shodan-idb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := load(t, tc.options, tc.services)
			if !host.commands.Has("passive") || host.commands.Has("scan") {
				t.Fatalf("commands=%v", host.commands.Names())
			}
			if docs := host.commands.UsageDocs(); !strings.Contains(docs, "Sources available now: "+tc.want+"\n") || !strings.Contains(docs, docsPath) {
				t.Fatalf("prompt entry lacks sources or reference:\n%s", docs)
			}
			help := host.help(t)
			if !strings.Contains(help, "Available now: "+tc.want+"\n") {
				t.Fatalf("help does not list exactly %q:\n%s", tc.want, help)
			}
			body, err := host.files.Read(t.Context(), host.commands.DescriptionPath("passive"))
			if err != nil || len(body) == 0 {
				t.Fatalf("docs: %v", err)
			}
			// `cyber-scan passive --ai` attaches the playbook through the store.
			if concept, ok, err := host.store.ReadVirtualBody(scan.ScannerConceptURI("passive")); !ok || err != nil || !strings.Contains(concept, "# Passive") {
				t.Fatalf("skill store concept ok=%v err=%v", ok, err)
			}
			listed := map[string]bool{}
			for _, source := range strings.Split(tc.want, ", ") {
				listed[source] = true
			}
			for _, source := range []string{"fofa", "hunter", "shodan", "shodan-idb", "censys", "quake", "zoomeye"} {
				if !listed[source] && strings.Contains(help, "-s "+source+" ") {
					t.Fatalf("help names unconfigured source %q:\n%s", source, help)
				}
				// The playbook is source-agnostic; -h is the only source list.
				if strings.Contains(strings.ToLower(string(body)), source) {
					t.Fatalf("docs name source %q", source)
				}
			}
			if err := host.set.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if host.commands.Has("passive") {
				t.Fatal("command survived extension close")
			}
			if _, err := host.files.Read(t.Context(), docsPath); err == nil {
				t.Fatal("docs survived extension close")
			}
		})
	}
}
