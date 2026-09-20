package config

import "testing"

func boolPtr(value bool) *bool { return &value }

func TestOutputPresetPolicies(t *testing.T) {
	tests := []struct {
		name   string
		option Option
		want   OutputPolicy
	}{
		{name: "default", want: OutputPolicyForPreset(OutputPresetDefault)},
		{
			name:   "verbose CLI",
			option: Option{MiscOptions: MiscOptions{Verbose: []bool{true}}},
			want:   OutputPolicyForPreset(OutputPresetVerbose),
		},
		{
			name:   "full CLI",
			option: Option{MiscOptions: MiscOptions{Verbose: []bool{true, true}}},
			want:   OutputPolicyForPreset(OutputPresetFull),
		},
		{
			name:   "quiet CLI",
			option: Option{MiscOptions: MiscOptions{Quiet: true, Verbose: []bool{true, true}}},
			want:   OutputPolicyForPreset(OutputPresetQuiet),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveOutputPolicy(&tc.option)
			if err != nil {
				t.Fatal(err)
			}
			if !outputPoliciesEqual(got, tc.want) || got.Custom != tc.want.Custom {
				t.Fatalf("policy = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestOutputConfigOverridesPreset(t *testing.T) {
	option := Option{OutputOptions: OutputOptions{
		Preset:        "verbose",
		Reasoning:     "hidden",
		ToolArguments: "full",
		ToolResults:   "hidden",
		LiveStatus:    boolPtr(false),
		Usage:         boolPtr(false),
	}}

	got, err := ResolveOutputPolicy(&option)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reasoning != OutputDetailHidden || got.ToolArguments != OutputDetailFull ||
		got.ToolResults != OutputDetailHidden || got.LiveStatus || got.Usage || !got.Custom {
		t.Fatalf("custom policy = %#v", got)
	}
}

func TestOutputCLIOverridesEntireConfig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verbose []bool
		preset  OutputPreset
	}{
		{name: "verbose", verbose: []bool{true}, preset: OutputPresetVerbose},
		{name: "full", verbose: []bool{true, true}, preset: OutputPresetFull},
	} {
		t.Run(tc.name, func(t *testing.T) {
			option := Option{
				OutputOptions: OutputOptions{
					Preset: "default", Reasoning: "hidden", ToolCalls: "hidden",
					ToolArguments: "full", ToolResults: "hidden",
					LiveStatus: boolPtr(false), Usage: boolPtr(false),
				},
				MiscOptions: MiscOptions{Verbose: tc.verbose},
			}

			got, err := ResolveOutputPolicy(&option)
			if err != nil {
				t.Fatal(err)
			}
			want := OutputPolicyForPreset(tc.preset)
			if !outputPoliciesEqual(got, want) || got.Custom {
				t.Fatalf("CLI policy = %#v, want %#v", got, want)
			}
		})
	}
}

func TestLoadedOutputPresetKeepsUnspecifiedPresetValues(t *testing.T) {
	path := writeTestConfig(t, t.TempDir(), `
output:
  preset: verbose
`)
	var option Option
	if err := LoadConfig(path, &option); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveOutputPolicy(&option)
	if err != nil {
		t.Fatal(err)
	}
	want := OutputPolicyForPreset(OutputPresetVerbose)
	if !outputPoliciesEqual(got, want) || got.Custom {
		t.Fatalf("loaded preset policy = %#v, want %#v", got, want)
	}
}

func TestOutputPolicyRejectsInvalidValues(t *testing.T) {
	tests := []OutputOptions{
		{Preset: "debug"},
		{Reasoning: "preview"},
		{ToolCalls: "full"},
		{ToolArguments: "compact"},
		{ToolResults: "compact"},
	}
	for _, opts := range tests {
		if _, err := ResolveOutputPolicy(&Option{OutputOptions: opts}); err == nil {
			t.Fatalf("ResolveOutputPolicy(%#v) succeeded", opts)
		}
	}
}

func TestMergeOutputOptionsKeepsLocalValues(t *testing.T) {
	dst := OutputOptions{Preset: "full", ToolResults: "hidden", LiveStatus: boolPtr(false)}
	src := OutputOptions{
		Preset: "verbose", Reasoning: "full", ToolCalls: "compact",
		ToolArguments: "preview", ToolResults: "full",
		LiveStatus: boolPtr(true), Usage: boolPtr(true),
	}
	mergeOutputOptions(&dst, &src)

	if dst.Preset != "full" || dst.ToolResults != "hidden" || dst.LiveStatus == nil || *dst.LiveStatus {
		t.Fatalf("local output values were overwritten: %#v", dst)
	}
	if dst.Reasoning != "full" || dst.ToolCalls != "compact" ||
		dst.ToolArguments != "preview" || dst.Usage == nil || !*dst.Usage {
		t.Fatalf("config output values were not merged: %#v", dst)
	}
}
