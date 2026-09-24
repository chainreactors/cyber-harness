package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/skills"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/console"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	"github.com/chainreactors/cyber/pkg/profile"
	"github.com/chainreactors/cyber/tools/scan"
)

type scannerMode struct {
	Provider profile.ProviderMode
	Agent    bool
}

func resolveScannerMode(rest []string, defaultVerify string) (scannerMode, []string, error) {
	if len(rest) == 0 {
		return scannerMode{}, nil, fmt.Errorf("missing scanner command")
	}
	if rest[0] != "scan" {
		return scannerMode{}, rest, nil
	}
	verifyMode, explicit := scannerVerifyMode(rest[1:], defaultVerify)
	sniperEnabled := hasScannerFlag(rest[1:], "--sniper")
	deepEnabled := hasScannerFlag(rest[1:], "--deep")
	aiSkillRequested := sniperEnabled || deepEnabled

	mode := scannerMode{}

	if aiSkillRequested {
		mode.Provider = profile.ProviderRequired
		mode.Agent = true
	}

	switch verifyMode {
	case "auto":
		if !aiSkillRequested {
			mode.Provider = profile.ProviderOptional
		}
		mode.Agent = explicit || aiSkillRequested
		return mode, removeScannerFlag(rest, "--verify"), nil
	case "off":
		if explicit {
			return mode, replaceOrAppendScannerFlag(rest, "--verify", "off"), nil
		}
		return mode, rest, nil
	case "low", "medium", "high", "critical":
		if aiSkillRequested || explicit {
			mode.Provider = profile.ProviderRequired
		} else {
			mode.Provider = profile.ProviderOptional
		}
		mode.Agent = explicit || aiSkillRequested
		return mode, rest, nil
	default:
		if explicit {
			return scannerMode{}, nil, fmt.Errorf("invalid --verify value %q: expected auto, off, low, medium, high, or critical", verifyMode)
		}
		return mode, rest, nil
	}
}

func hasScannerFlag(args []string, long string) bool {
	for _, arg := range args {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			return true
		}
	}
	return false
}

func shouldStreamScannerOutput(rest []string) bool {
	if len(rest) == 0 || rest[0] != "scan" {
		return false
	}
	if isDirectScannerJSONOutput(rest) {
		return false
	}
	for _, arg := range rest[1:] {
		if arg == "--report" {
			return false
		}
		if strings.HasPrefix(arg, "--report=") {
			value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--report=")))
			if value != "false" && value != "0" && value != "no" {
				return false
			}
		}
	}
	return true
}

func isDirectScannerJSONOutput(rest []string) bool {
	if len(rest) == 0 || !scannerext.Available(rest[0]) {
		return false
	}
	args := rest[1:]
	switch rest[0] {
	case "gogo", "zombie":
		value, ok := scannerFlagValue(args, "-o", "--output")
		value = strings.ToLower(strings.TrimSpace(value))
		return ok && (value == "jl" || value == "json" || value == "jsonl")
	case "katana":
		return scannerBoolFlagEnabled(args, "-j", "-jsonl", "--jsonl")
	case "scan", "spray", "neutron", "proton":
		return scannerBoolFlagEnabled(args, "-j", "--json", "--jsonl")
	}
	return false
}

func scannerBoolFlagEnabled(args []string, names ...string) bool {
	for _, arg := range args {
		key, value, hasValue := strings.Cut(arg, "=")
		for _, name := range names {
			if key != name {
				continue
			}
			if !hasValue {
				return true
			}
			value = strings.ToLower(strings.TrimSpace(value))
			return value != "false" && value != "0" && value != "no"
		}
	}
	return false
}

func scannerFlagValue(args []string, names ...string) (string, bool) {
	for i := 0; i < len(args); i++ {
		key, value, hasValue := strings.Cut(args[i], "=")
		for _, name := range names {
			if key != name {
				continue
			}
			if hasValue {
				return value, true
			}
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		}
	}
	return "", false
}

func scannerVerifyMode(args []string, defaultVerify string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, value, hasValue := strings.Cut(arg, "=")
		if key != "--verify" {
			continue
		}
		if hasValue {
			return strings.ToLower(strings.TrimSpace(value)), true
		}
		if i+1 < len(args) {
			return strings.ToLower(strings.TrimSpace(args[i+1])), true
		}
		return "", true
	}
	return defaultVerifyMode(defaultVerify), false
}

func replaceOrAppendScannerFlag(args []string, flag, value string) []string {
	out := append([]string(nil), args...)
	for i := 1; i < len(out); i++ {
		arg := out[i]
		key, _, hasValue := strings.Cut(arg, "=")
		if key != flag {
			continue
		}
		if hasValue {
			out[i] = flag + "=" + value
			return out
		}
		if i+1 < len(out) {
			out[i+1] = value
			return out
		}
		out = append(out, value)
		return out
	}
	return append(out, flag+"="+value)
}

func defaultVerifyMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "off"
	}
	return value
}

func removeScannerFlag(args []string, flag string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, _, hasValue := strings.Cut(arg, "=")
		if key != flag {
			out = append(out, arg)
			continue
		}
		if !hasValue && i+1 < len(args) {
			i++
		}
	}
	return out
}

func runScannerWithAgent(ctx context.Context, option *cfg.Option, runtime *agentsession.Runtime, scannerArgs []string, logger telemetry.Logger) error {
	if runtime == nil {
		return fmt.Errorf("scanner Agent runtime is unavailable")
	}
	if provider, _ := runtime.ProviderState(); provider == nil {
		return fmt.Errorf("--ai requires a configured LLM provider")
	}
	lock, err := acquirePIDLock(agentPIDFilePath(), logger)
	if err != nil {
		return err
	}
	defer lock.Release()

	intent, err := resolveScannerIntent(option, runtime.Skills(), scannerArgs[0])
	if err != nil {
		return err
	}
	prompt := scan.FormatAgentTaskPrompt(scannerArgs, intent)
	return console.RunTask(ctx, runtime, option, "scanner", "scanner", strings.Join(scannerArgs, " "), agentsession.RunInput{Content: []*aop.Content{aop.Text(prompt)}}, nil)
}

func resolveScannerIntent(option *cfg.Option, store *skills.Store, command string) (string, error) {
	var sections []string
	if conceptURI := scan.ScannerConceptURI(command); conceptURI != "" && scannerext.Available(command) {
		if body, ok, err := store.ReadVirtualBody(conceptURI); err == nil && ok && body != "" {
			sections = append(sections, skills.FormatVirtualInvocation(command, conceptURI, body))
		}
	}
	intent, err := cfg.ResolvePrompt(option.Prompt)
	if err != nil {
		return "", err
	}
	if intent == "" && option.TaskFile != "" {
		data, err := os.ReadFile(option.TaskFile)
		if err != nil {
			return "", fmt.Errorf("read task file: %w", err)
		}
		intent = strings.TrimSpace(string(data))
	}
	if intent == "" {
		intent = "Process the scanner output according to the user's intent. If no specific intent is provided, briefly explain the important evidence in the output."
	}
	intent, err = store.ApplySelected(intent, scan.FilterAutoSkill(option.Skills, command))
	if err != nil {
		return "", err
	}
	return strings.Join(append(sections, intent), "\n\n"), nil
}
