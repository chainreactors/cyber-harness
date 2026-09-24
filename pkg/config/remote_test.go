package config

import (
	"maps"
	"reflect"
	"testing"

	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/proto"
)

func TestDistributedRuntimeDoesNotMutateHostFileState(t *testing.T) {
	c := isolatedContext(t)
	putConfig(t, c.UserFile(), "old_fixture:\n  name: local\nllm:\n  providers:\n    - id: local\n      provider: openai\n      model: local-model\n")
	host := &Option{Context: c, Sections: fixtureSections(t), Explicit: map[string]bool{}}
	if _, err := ResolveRuntimeConfig(host); err != nil {
		t.Fatal(err)
	}
	before := CloneDocument(host.Snapshot.Effective)
	sources := maps.Clone(host.Snapshot.Sources)
	exts, err := ValuesToProto(Values{"fixture": {"name": "remote"}})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := ResolveDistributedRuntime(&types.DistributeConfig{
		Extensions: exts,
		Llm:        &types.LLMConfig{ActiveProfile: "remote", Providers: []*types.LLMProviderConfig{{Id: "remote", Provider: "openai", Model: "remote-model"}}},
	}, host)
	if err != nil {
		t.Fatal(err)
	}
	value, err := Get[*fixtureOptions](remote.Resolved, "fixture")
	if err != nil || value.Name != "remote" || remote.Model != "remote-model" {
		t.Fatal("distributed configuration did not replace local values", err)
	}
	if !reflect.DeepEqual(host.Snapshot.Effective, before) || !reflect.DeepEqual(host.Snapshot.Sources, sources) {
		t.Fatal("distributed resolution mutated host file state")
	}
	if remote.Snapshot != nil {
		t.Fatal("distributed configuration retained unrelated local file layers")
	}
	local, err := Get[*fixtureOptions](host.Resolved, "fixture")
	if err != nil || local.Name != "local" {
		t.Fatal("distributed resolution mutated host extension options", err)
	}
}

func TestDistributedLLMOverridesLocalFlagsEnvironmentAndDefaults(t *testing.T) {
	withDefaults(t, func() {
		DefaultModel, DefaultAPIKey, DefaultBaseURL = "compiled-model", "compiled-key", "https://compiled.invalid/v1"
		c := isolatedContext(t)
		c.LookupEnv = func(key string) (string, bool) {
			value, ok := map[string]string{
				"CYBER_MODEL": "env-model", "CYBER_API_KEY": "env-key", "CYBER_PROVIDER": "invalid-local-provider",
				"CYBER_BASE_URL": "https://env.invalid/v1", "OPENAI_API_KEY": "fallback-key",
			}[key]
			return value, ok
		}
		host := &Option{Context: c, Explicit: map[string]bool{"profile": true, "model": true, "api-key": true},
			LLMOptions:  LLMOptions{ActiveProfile: "stale-local-profile", Model: "cli-model", APIKey: "cli-key"},
			NodeOptions: NodeOptions{NodeID: "local-node"},
		}
		remote, err := ResolveDistributedRuntime(&types.DistributeConfig{
			Llm: &types.LLMConfig{ActiveProfile: "server", Providers: []*types.LLMProviderConfig{{
				Id: "server", Provider: "openai", BaseUrl: "https://server.invalid/v1", Model: "server-model", ApiKey: "server-key",
			}}},
		}, host)
		if err != nil {
			t.Fatal(err)
		}
		p := ProviderConfig(remote)
		if p.Model != "server-model" || p.APIKey != "server-key" || p.BaseURL != "https://server.invalid/v1" || remote.ActiveProfile != "server" {
			t.Fatal("local settings overrode the server LLM")
		}
		if host.ActiveProfile != "stale-local-profile" || !host.Explicit["profile"] || remote.NodeID != "local-node" {
			t.Fatal("remote resolution mutated host settings or identity")
		}
		cleared, err := ResolveDistributedRuntime(&types.DistributeConfig{}, remote)
		if err != nil {
			t.Fatal(err)
		}
		p = ProviderConfig(cleared)
		if p.Model != "" || p.APIKey != "" || p.BaseURL != "" || len(FallbackProviderConfigs(cleared)) != 0 {
			t.Fatal("empty server LLM fell back to local settings")
		}
	})
}

func TestDistributedLLMPreservesInferredNonModelOverrides(t *testing.T) {
	host := &Option{Context: isolatedContext(t),
		LLMOptions:   LLMOptions{ActiveProfile: "stale", APIKey: "local-key"},
		AgentOptions: AgentOptions{Timeout: 77},
	}
	remote, err := ResolveDistributedRuntime(&types.DistributeConfig{
		Agent: &types.AgentConfig{Timeout: proto.Int32(99)},
		Llm:   &types.LLMConfig{Providers: []*types.LLMProviderConfig{{Id: "remote", Provider: "openai", Model: "remote-model", ApiKey: "remote-key"}}},
	}, host)
	if err != nil {
		t.Fatal(err)
	}
	if remote.Timeout != 77 || remote.APIKey != "remote-key" || remote.Model != "remote-model" || host.Explicit != nil {
		t.Fatal("implicit host overrides or remote model ownership changed")
	}
}

func TestDistributedRuntimeMatchesFileAndReplacesOldValues(t *testing.T) {
	for _, key := range []string{"CYBER_MODEL", "CYBER_PROVIDER", "CYBER_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		t.Setenv(key, "")
	}
	distributed := &types.DistributeConfig{
		Agent:   &types.AgentConfig{Timeout: proto.Int32(0), Heartbeat: 3, EvalCriteria: "goal", EvalModel: "judge", EvalRounds: "4", CaptureProviderFrames: true},
		Traffic: &types.TrafficConfig{BodyStorage: "disk", BodyMaxBytes: 1024, BodyRetentionBytes: 4096},
	}
	data, err := MarshalDistributeConfigYAML(distributed)
	if err != nil {
		t.Fatal(err)
	}
	path := writeTestConfig(t, t.TempDir(), string(data))
	file := Option{Explicit: map[string]bool{}, MiscOptions: MiscOptions{ConfigFile: path}}
	if _, err := ResolveRuntimeConfig(&file); err != nil {
		t.Fatal(err)
	}
	host := &Option{Explicit: map[string]bool{}, AgentOptions: AgentOptions{Timeout: 99, Heartbeat: 88, ServerURL: "http://hub"}, NodeOptions: NodeOptions{NodeID: "local"}}
	remote, err := ResolveDistributedRuntime(distributed, host)
	if err != nil {
		t.Fatal(err)
	}
	if remote.Timeout != file.Timeout || remote.Timeout != 0 || remote.Heartbeat != 3 || remote.EvalCriteria != "goal" || remote.EvalModel != "judge" || remote.EvalRounds != "4" || !remote.CaptureProviderFrames || remote.TrafficOptions != file.TrafficOptions {
		t.Fatalf("lost distributed values: %+v", remote)
	}
	if remote.NodeID != "local" || remote.ServerURL != "http://hub" {
		t.Fatal("host identity changed")
	}
	cleared, err := ResolveDistributedRuntime(&types.DistributeConfig{}, remote)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Heartbeat != 0 || cleared.EvalCriteria != "" || cleared.CaptureProviderFrames || cleared.BodyStorage != "" || cleared.Timeout != 3600 {
		t.Fatalf("old configuration survived replacement: %+v", cleared)
	}
}

func TestDistributedReplacementUsesSchemaOwnership(t *testing.T) {
	c := isolatedContext(t)
	putConfig(t, c.UserFile(), `misc:
  quiet: true
  no_color: true
  data_dir: local-data
output:
  preset: full
  live_status: false
agent:
  server_url: http://local-hub
  transport: web
  timeout: 77
  heartbeat: 5
node:
  id: local-id
  name: local-name
old_fixture:
  name: local-extension
  count: 8
`)
	host := &Option{Context: c, Sections: fixtureSections(t), Explicit: map[string]bool{}}
	if _, err := ResolveRuntimeConfig(host); err != nil {
		t.Fatal(err)
	}
	extensions, err := ValuesToProto(Values{"fixture": {"name": "remote-extension", "count": 0}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := ResolveDistributedRuntime(&types.DistributeConfig{
		Agent:      &types.AgentConfig{Timeout: proto.Int32(0), Heartbeat: 3},
		Node:       &types.NodeConfig{Id: "remote-id", Name: "remote-name"},
		Extensions: extensions,
	}, host)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.MiscOptions, host.MiscOptions) || !reflect.DeepEqual(next.OutputOptions, host.OutputOptions) || next.NodeOptions != host.NodeOptions || next.ServerURL != host.ServerURL || next.Transport != host.Transport {
		t.Fatal("distributed update changed locally owned settings")
	}
	if next.Timeout != 0 || next.Heartbeat != 3 {
		t.Fatal("remote replacement lost zero or inherited old shared fields")
	}
	value, err := Get[*fixtureOptions](next.Resolved, "fixture")
	if err != nil || value.Name != "remote-extension" || value.Count != 0 {
		t.Fatal("remote extension did not replace local extension", err)
	}
	cleared, err := ResolveDistributedRuntime(&types.DistributeConfig{}, next)
	if err != nil {
		t.Fatal(err)
	}
	value, err = Get[*fixtureOptions](cleared.Resolved, "fixture")
	if err != nil || value.Name != "default" || value.Count != 3 || cleared.Heartbeat != 0 || cleared.Timeout != 3600 {
		t.Fatal("removed distributed values survived replacement", err)
	}
	if !reflect.DeepEqual(cleared.OutputOptions, host.OutputOptions) || cleared.NodeOptions != host.NodeOptions || cleared.ServerURL != host.ServerURL {
		t.Fatal("empty remote replacement lost local settings")
	}
}

func TestDistributedReplacementRetainsExplicitZeroAndExtensionCLI(t *testing.T) {
	c := isolatedContext(t)
	putConfig(t, c.UserFile(), "agent:\n  timeout: 77\nold_fixture:\n  count: 5\n")
	host := &Option{Context: c, Sections: fixtureSections(t), Explicit: map[string]bool{"timeout": true},
		Extensions: Values{"fixture": {"count": 0, "enabled": false}},
	}
	if _, err := ResolveRuntimeConfig(host); err != nil {
		t.Fatal(err)
	}
	exts, err := ValuesToProto(Values{"fixture": {"name": "remote", "count": 9, "enabled": true}})
	if err != nil {
		t.Fatal(err)
	}
	remote, err := ResolveDistributedRuntime(&types.DistributeConfig{Agent: &types.AgentConfig{Timeout: proto.Int32(99)}, Extensions: exts}, host)
	if err != nil {
		t.Fatal(err)
	}
	value, err := Get[*fixtureOptions](remote.Resolved, "fixture")
	if err != nil || remote.Timeout != 0 || value.Count != 0 || value.Enabled || value.Name != "remote" {
		t.Fatal("distributed replacement changed explicit CLI values", err)
	}
	invalid, err := ValuesToProto(Values{"fixture": {"typo": true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDistributedRuntime(&types.DistributeConfig{Extensions: invalid}, host); err == nil {
		t.Fatal("distributed input bypassed extension validation")
	}
}
