package config

import (
	"testing"

	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/proto"
)

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
