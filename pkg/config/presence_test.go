package config

import (
	flags "github.com/jessevdk/go-flags"
	"testing"
)

func TestRuntimeFileConfigurationRetainsAllSections(t *testing.T) {
	path := writeTestConfig(t, t.TempDir(), `agent:
  timeout: 77
  eval_criteria: audit-goal
  eval_model: evaluator
  eval_rounds: "3"
  heartbeat: 4
  capture_provider_frames: true
cyberhub:
  mitm: false
traffic:
  body_storage: disk
  body_max_bytes: 1024
  body_retention_bytes: 4096
misc:
  quiet: true
`)
	option := Option{AgentOptions: AgentOptions{Timeout: 3600}, Explicit: map[string]bool{}}
	option.ConfigFile = path
	if _, err := LoadAndApplyConfig(&option); err != nil {
		t.Fatal(err)
	}
	if option.Timeout != 77 || option.EvalCriteria != "audit-goal" || option.EvalModel != "evaluator" || option.EvalRounds != "3" || option.Heartbeat != 4 || !option.CaptureProviderFrames {
		t.Fatalf("agent configuration lost: %+v", option.AgentOptions)
	}
	if option.Mitm == nil || *option.Mitm || option.BodyStorage != "disk" || option.BodyMaxBytes != 1024 || option.BodyRetentionBytes != 4096 || !option.Quiet {
		t.Fatalf("storage or boolean configuration lost: %+v", option)
	}
}

func TestExplicitZeroAndEmptyOverrideFileAndEnvironment(t *testing.T) {
	path := writeTestConfig(t, t.TempDir(), "agent:\n  timeout: 77\n  eval_criteria: goal\n  heartbeat: 4\nllm:\n  model: file-model\n")
	var option Option
	parser := flags.NewParser(&option, flags.Default&^flags.PrintErrors)
	if _, err := parser.ParseArgs([]string{"--timeout=0", "--heartbeat=0", "--eval=", "--model="}); err != nil {
		t.Fatal(err)
	}
	CaptureExplicitFlags(&option, parser)
	option.ConfigFile = path
	t.Setenv("CYBER_MODEL", "environment-model")
	if _, err := ResolveRuntimeConfig(&option); err != nil {
		t.Fatal(err)
	}
	if option.Timeout != 0 || option.Heartbeat != 0 || option.EvalCriteria != "" || option.Model != "" {
		t.Fatalf("explicit values were replaced: %+v", option.AgentOptions)
	}
}

func TestFileZeroIsNotReplacedByDefaults(t *testing.T) {
	path := writeTestConfig(t, t.TempDir(), "agent:\n  timeout: 0\n")
	option := Option{Explicit: map[string]bool{}, AgentOptions: AgentOptions{Timeout: 3600}}
	option.ConfigFile = path
	if _, err := ResolveRuntimeConfig(&option); err != nil {
		t.Fatal(err)
	}
	if option.Timeout != 0 {
		t.Fatalf("timeout = %d", option.Timeout)
	}
}
