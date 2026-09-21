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
	credential := "local-credential"
	c.LookupEnv = func(key string) (string, bool) { return credential, key == "SHODAN_API_KEY" }
	putConfig(t, c.UserFile(), "old_fixture:\n  name: local\nllm:\n  providers:\n    - id: local\n      provider: openai\n      model: local-model\n")
	host := &Option{Context: c, Sections: fixtureSections(t), Explicit: map[string]bool{}}
	if _, err := ResolveRuntimeConfig(host); err != nil {
		t.Fatal(err)
	}
	before := CloneDocument(host.Snapshot.Effective)
	sources := maps.Clone(host.Snapshot.Sources)
	credential = "replacement-credential"
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
	if host.UncoverCredentials["SHODAN_API_KEY"] != "local-credential" || remote.UncoverCredentials["SHODAN_API_KEY"] != "replacement-credential" {
		t.Fatal("environment resolution shared mutable credential state")
	}
	local, err := Get[*fixtureOptions](host.Resolved, "fixture")
	if err != nil || local.Name != "local" {
		t.Fatal("distributed resolution mutated host extension options", err)
	}
}

func TestDistributedRuntimeMatchesFileAndReplacesOldValues(t *testing.T) {
	for _, key := range []string{"CYBER_MODEL", "CYBER_PROVIDER", "CYBER_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		t.Setenv(key, "")
	}
	distributed := &types.DistributeConfig{
		Agent:    &types.AgentConfig{Timeout: proto.Int32(0), Heartbeat: 3, EvalCriteria: "goal", EvalModel: "judge", EvalRounds: "4", CaptureProviderFrames: true},
		Cyberhub: &types.CyberhubConfig{Mitm: proto.Bool(false)},
		Traffic:  &types.TrafficConfig{BodyStorage: "disk", BodyMaxBytes: 1024, BodyRetentionBytes: 4096},
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
	if remote.Timeout != file.Timeout || remote.Timeout != 0 || remote.Heartbeat != 3 || remote.EvalCriteria != "goal" || remote.EvalModel != "judge" || remote.EvalRounds != "4" || !remote.CaptureProviderFrames || remote.Mitm == nil || *remote.Mitm || remote.TrafficOptions != file.TrafficOptions {
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
