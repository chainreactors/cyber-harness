package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	provider "github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/skills"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	scannerext "github.com/chainreactors/cyber/exts/scanner"
	taskcli "github.com/chainreactors/cyber/pkg/cli/task"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/tools/scan"
	"github.com/chainreactors/cyber/tools/toolargs"
)

type scannerMode struct {
	Provider provider.StartupMode
	Agent    bool
}

// Resolve only the model configuration used by this command. Help and atomic
// tools must remain usable when a model profile or scan default needs repair.
func resolveScannerRuntimeConfig(option *cfg.Option, args []string) (string, error) {
	return cfg.ResolveCommandRuntimeConfig(option, func(loaded *cfg.Option) (bool, error) {
		if cfg.IsScannerHelpRequest(args) {
			return false, nil
		}
		if loaded.AI {
			return true, nil
		}
		mode, _, err := configuredScannerMode(loaded, args)
		return mode.Provider != provider.StartupDisabled, err
	})
}

func configuredScannerMode(option *cfg.Option, args []string) (scannerMode, []string, error) {
	var verify string
	if len(args) > 0 && args[0] == "scan" {
		scanOptions, err := scannerext.ReadScan(option)
		if err != nil {
			return scannerMode{}, nil, err
		}
		verify = scanOptions.Verify
	}
	return resolveScannerMode(args, verify)
}

func resolveScannerMode(rest []string, defaultVerify string) (scannerMode, []string, error) {
	if len(rest) == 0 {
		return scannerMode{}, nil, fmt.Errorf("missing scanner command")
	}
	if rest[0] != "scan" {
		return scannerMode{}, rest, nil
	}
	flags := rest[1:]
	if end := slices.Index(flags, "--"); end >= 0 {
		flags = flags[:end]
	}
	verifyMode, explicit := scannerFlagValue(flags, "--verify")
	if !explicit {
		verifyMode = defaultVerify
	}
	verifyMode = strings.ToLower(strings.TrimSpace(verifyMode))
	if toolargs.HasFlag(flags, "--deep") {
		return scannerMode{}, nil, fmt.Errorf("--deep is no longer supported; use --mode full for full scanning")
	}
	if err := scan.ValidateVerify(verifyMode); err != nil {
		return scannerMode{}, nil, err
	}
	if explicit && verifyMode == "" {
		return scannerMode{}, nil, fmt.Errorf("--verify requires on or off")
	}
	mode := scannerMode{Provider: provider.StartupOptional}
	if verifyMode == "on" || scannerBoolFlagEnabled(flags, "--sniper") {
		mode.Provider = provider.StartupRequired
		mode.Agent = true
	}
	if verifyMode != "" {
		rest = replaceOrAppendScannerFlag(rest, "--verify", verifyMode)
	}
	return mode, rest, nil
}

func shouldStreamScannerOutput(rest []string) bool {
	if len(rest) == 0 || rest[0] != "scan" {
		return false
	}
	if isDirectScannerJSONOutput(rest) {
		return false
	}
	for _, arg := range rest[1:] {
		if arg == "--" {
			break
		}
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
	if len(rest) == 0 || !productScannerAvailable(rest[0]) {
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
		if arg == "--" {
			break
		}
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
		if args[i] == "--" {
			break
		}
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

func replaceOrAppendScannerFlag(args []string, flag, value string) []string {
	out := append([]string(nil), args...)
	for i := 1; i < len(out); i++ {
		arg := out[i]
		if arg == "--" {
			return slices.Insert(out, i, flag+"="+value)
		}
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

func runScannerWithAgent(ctx context.Context, option *cfg.Option, runtime *agentsession.Runtime, scannerArgs []string, logger telemetry.Logger, outputTask *taskcli.Output, finish func(error) error) error {
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
	return outputTask.Run(ctx, runtime, "scanner", "scanner", strings.Join(scannerArgs, " "), agentsession.RunInput{Content: []*aop.Content{aop.Text(prompt)}}, finish)
}

func resolveScannerIntent(option *cfg.Option, store *skills.Store, command string) (string, error) {
	var sections []string
	if conceptURI := scan.ScannerConceptURI(command); conceptURI != "" && productScannerAvailable(command) {
		if body, ok, err := store.ReadVirtualBody(conceptURI); err == nil && ok && body != "" {
			sections = append(sections, skills.FormatVirtualInvocation(command, conceptURI, body))
		}
	}
	intent, err := cfg.ResolveTaskPrompt(option)
	if err != nil {
		return "", err
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
