package config

import (
	"fmt"
	"strings"
)

type OutputOptions struct {
	Preset        string `config:"preset" default:"default" description:"Output preset: default, verbose, or full"`
	Reasoning     string `config:"reasoning" default:"hidden" config_optional:"true" description:"Reasoning output: hidden or full"`
	ToolCalls     string `config:"tool_calls" default:"compact" config_optional:"true" description:"Tool call output: hidden or compact"`
	ToolArguments string `config:"tool_arguments" default:"hidden" config_optional:"true" description:"Tool argument output: hidden, preview, or full"`
	ToolResults   string `config:"tool_results" default:"hidden" config_optional:"true" description:"Tool result output: hidden, preview, or full"`
	LiveStatus    *bool  `config:"live_status" default:"true" config_optional:"true" description:"Show the transient thinking/tooling/talking status"`
	Usage         *bool  `config:"usage" default:"true" config_optional:"true" description:"Show token and context usage in the live status"`
}

type OutputDetail string

const (
	OutputDetailHidden  OutputDetail = "hidden"
	OutputDetailPreview OutputDetail = "preview"
	OutputDetailFull    OutputDetail = "full"
)

type OutputCalls string

const (
	OutputCallsHidden  OutputCalls = "hidden"
	OutputCallsCompact OutputCalls = "compact"
)

type OutputPreset string

const (
	OutputPresetDefault OutputPreset = "default"
	OutputPresetVerbose OutputPreset = "verbose"
	OutputPresetFull    OutputPreset = "full"
	OutputPresetQuiet   OutputPreset = "quiet"
)

type OutputPolicy struct {
	Preset        OutputPreset
	Reasoning     OutputDetail
	ToolCalls     OutputCalls
	ToolArguments OutputDetail
	ToolResults   OutputDetail
	LiveStatus    bool
	Usage         bool
	Custom        bool
}

func (p OutputPolicy) Quiet() bool {
	return p.Preset == OutputPresetQuiet
}

func (p OutputPolicy) ShowReasoning() bool {
	return !p.Quiet() && p.Reasoning == OutputDetailFull
}

func OutputPolicyForPreset(preset OutputPreset) OutputPolicy {
	switch preset {
	case OutputPresetVerbose:
		return OutputPolicy{
			Preset: preset, Reasoning: OutputDetailFull, ToolCalls: OutputCallsCompact,
			ToolArguments: OutputDetailPreview, ToolResults: OutputDetailPreview,
			LiveStatus: true, Usage: true,
		}
	case OutputPresetFull:
		return OutputPolicy{
			Preset: preset, Reasoning: OutputDetailFull, ToolCalls: OutputCallsCompact,
			ToolArguments: OutputDetailPreview, ToolResults: OutputDetailFull,
			LiveStatus: true, Usage: true,
		}
	case OutputPresetQuiet:
		return OutputPolicy{
			Preset: preset, Reasoning: OutputDetailHidden, ToolCalls: OutputCallsHidden,
			ToolArguments: OutputDetailHidden, ToolResults: OutputDetailHidden,
		}
	default:
		return OutputPolicy{
			Preset: OutputPresetDefault, Reasoning: OutputDetailHidden, ToolCalls: OutputCallsCompact,
			ToolArguments: OutputDetailHidden, ToolResults: OutputDetailHidden,
			LiveStatus: true, Usage: true,
		}
	}
}

func OutputPolicyForLevel(level int) OutputPolicy {
	switch {
	case level < 0:
		return OutputPolicyForPreset(OutputPresetQuiet)
	case level == 1:
		return OutputPolicyForPreset(OutputPresetVerbose)
	case level >= 2:
		return OutputPolicyForPreset(OutputPresetFull)
	default:
		return OutputPolicyForPreset(OutputPresetDefault)
	}
}

func ResolveOutputPolicy(option *Option) (OutputPolicy, error) {
	if option != nil {
		if option.Quiet {
			return OutputPolicyForPreset(OutputPresetQuiet), nil
		}
		if len(option.Verbose) > 0 {
			return OutputPolicyForLevel(len(option.Verbose)), nil
		}
	}

	opts := OutputOptions{}
	if option != nil {
		opts = option.OutputOptions
	}
	preset, err := parseOutputPreset(opts.Preset)
	if err != nil {
		return OutputPolicy{}, err
	}
	base := OutputPolicyForPreset(preset)
	policy := base

	if opts.Reasoning != "" {
		policy.Reasoning, err = parseOutputDetail("reasoning", opts.Reasoning, false)
		if err != nil {
			return OutputPolicy{}, err
		}
	}
	if opts.ToolCalls != "" {
		policy.ToolCalls, err = parseOutputCalls(opts.ToolCalls)
		if err != nil {
			return OutputPolicy{}, err
		}
	}
	if opts.ToolArguments != "" {
		policy.ToolArguments, err = parseOutputDetail("tool_arguments", opts.ToolArguments, true)
		if err != nil {
			return OutputPolicy{}, err
		}
	}
	if opts.ToolResults != "" {
		policy.ToolResults, err = parseOutputDetail("tool_results", opts.ToolResults, true)
		if err != nil {
			return OutputPolicy{}, err
		}
	}
	if opts.LiveStatus != nil {
		policy.LiveStatus = *opts.LiveStatus
	}
	if opts.Usage != nil {
		policy.Usage = *opts.Usage
	}
	policy.Custom = !outputPoliciesEqual(policy, base)
	return policy, nil
}

func parseOutputPreset(value string) (OutputPreset, error) {
	switch preset := OutputPreset(strings.ToLower(strings.TrimSpace(value))); preset {
	case "", OutputPresetDefault:
		return OutputPresetDefault, nil
	case OutputPresetVerbose, OutputPresetFull:
		return preset, nil
	default:
		return "", fmt.Errorf("output.preset must be default, verbose, or full, got %q", value)
	}
}

func parseOutputDetail(field, value string, preview bool) (OutputDetail, error) {
	detail := OutputDetail(strings.ToLower(strings.TrimSpace(value)))
	if detail == OutputDetailHidden || detail == OutputDetailFull || (preview && detail == OutputDetailPreview) {
		return detail, nil
	}
	allowed := "hidden or full"
	if preview {
		allowed = "hidden, preview, or full"
	}
	return "", fmt.Errorf("output.%s must be %s, got %q", field, allowed, value)
}

func parseOutputCalls(value string) (OutputCalls, error) {
	calls := OutputCalls(strings.ToLower(strings.TrimSpace(value)))
	if calls == OutputCallsHidden || calls == OutputCallsCompact {
		return calls, nil
	}
	return "", fmt.Errorf("output.tool_calls must be hidden or compact, got %q", value)
}

func outputPoliciesEqual(a, b OutputPolicy) bool {
	return a.Preset == b.Preset &&
		a.Reasoning == b.Reasoning &&
		a.ToolCalls == b.ToolCalls &&
		a.ToolArguments == b.ToolArguments &&
		a.ToolResults == b.ToolResults &&
		a.LiveStatus == b.LiveStatus &&
		a.Usage == b.Usage
}

func mergeOutputOptions(dst, src *OutputOptions) {
	if dst.Preset == "" {
		dst.Preset = src.Preset
	}
	if dst.Reasoning == "" {
		dst.Reasoning = src.Reasoning
	}
	if dst.ToolCalls == "" {
		dst.ToolCalls = src.ToolCalls
	}
	if dst.ToolArguments == "" {
		dst.ToolArguments = src.ToolArguments
	}
	if dst.ToolResults == "" {
		dst.ToolResults = src.ToolResults
	}
	if dst.LiveStatus == nil {
		dst.LiveStatus = src.LiveStatus
	}
	if dst.Usage == nil {
		dst.Usage = src.Usage
	}
}
