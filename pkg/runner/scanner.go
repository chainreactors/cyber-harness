package runner

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/pidlock"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/console"
	"github.com/chainreactors/aiscan/pkg/edition"
	agentext "github.com/chainreactors/aiscan/pkg/exts/session"
	loopext "github.com/chainreactors/aiscan/pkg/exts/agent"
	"github.com/chainreactors/aiscan/skills"
	"github.com/chainreactors/aiscan/tools/scan"
)

func DirectScannerRuntimeFeatures(rest []string) (apppkg.RuntimeFeatures, []string, error) {
	return DirectScannerRuntimeFeaturesWithDefault(rest, cfg.DefaultVerify)
}

func DirectScannerRuntimeFeaturesWithDefault(rest []string, defaultVerify string) (apppkg.RuntimeFeatures, []string, error) {
	if len(rest) == 0 {
		return apppkg.RuntimeFeatures{}, nil, fmt.Errorf("missing scanner command")
	}
	if rest[0] != "scan" {
		return apppkg.RuntimeFeatures{}, rest, nil
	}
	verifyMode, explicit := scannerVerifyMode(rest[1:], defaultVerify)
	sniperEnabled := HasScannerFlag(rest[1:], "--sniper")
	deepEnabled := HasScannerFlag(rest[1:], "--deep")
	aiSkillRequested := sniperEnabled || deepEnabled

	features := apppkg.RuntimeFeatures{}

	if aiSkillRequested {
		features.ProviderEnabled = true
		features.ProviderOptional = false
		features.AIEnabled = true
		features.ScannerAI = true
	}

	switch verifyMode {
	case "auto":
		features.ProviderEnabled = true
		if !aiSkillRequested {
			features.ProviderOptional = true
		}
		features.AIEnabled = true
		features.ScannerAI = explicit || aiSkillRequested
		return features, removeScannerFlag(rest, "--verify"), nil
	case "off":
		if explicit {
			return features, replaceOrAppendScannerFlag(rest, "--verify", "off"), nil
		}
		return features, rest, nil
	case "low", "medium", "high", "critical":
		features.ProviderEnabled = true
		if !aiSkillRequested {
			features.ProviderOptional = !explicit
		}
		features.AIEnabled = true
		features.ScannerAI = explicit || aiSkillRequested
		return features, rest, nil
	default:
		if explicit {
			return apppkg.RuntimeFeatures{}, nil, fmt.Errorf("invalid --verify value %q: expected auto, off, low, medium, high, or critical", verifyMode)
		}
		return features, rest, nil
	}
}

func HasScannerFlag(args []string, long string) bool {
	for _, arg := range args {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			return true
		}
	}
	return false
}

func ShouldStreamScannerOutput(rest []string) bool {
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
	if len(rest) == 0 || !edition.Catalog().CLIAvailable(rest[0]) {
		return false
	}
	for _, arg := range rest[1:] {
		if arg == "-j" || arg == "--json" {
			return true
		}
		if strings.HasPrefix(arg, "--json=") {
			value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--json=")))
			return value != "false" && value != "0" && value != "no"
		}
	}
	return false
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

func runScannerWithAgent(ctx context.Context, option *cfg.Option, application *apppkg.App, scannerArgs []string, logger telemetry.Logger) error {
	if provider, _ := application.ProviderState(); provider == nil {
		return fmt.Errorf("--ai requires a configured LLM provider")
	}
	lock, err := pidlock.Acquire(pidlock.AgentPIDFilePath(), logger)
	if err != nil {
		return err
	}
	defer lock.Release()

	intent, err := resolveScannerIntent(option, application.Skills, scannerArgs[0])
	if err != nil {
		return err
	}
	loopResource, err := loopext.New(loopext.Config{Loop: agent.StandardLoop{}})
	if err != nil { return err }
	runtimeResource, err := agentext.New(agentext.Config{
		Application: application, Option: option, Logger: logger,
		Loop: loopResource.Runtime(),
		PromptConfig: &agentext.PromptConfig{
			Tools:            application.Tools,
			ScannerDocs:      application.Commands.UsageDocs(),
			Skills:           application.Skills.Skills,
			ScannerAgentMode: true,
			ScannerName:      scannerArgs[0],
		},
	})
	if err != nil {
		return err
	}
	runtime := runtimeResource.Runtime()
	runtimeSet, err := extension.New(
		extension.Entry{ID: "agent", Extension: loopResource},
		extension.Entry{ID: "session", DependsOn: []string{"agent"}, Extension: runtimeResource},
	)
	if err != nil {
		return err
	}
	if err := runtimeSet.Load(ctx); err != nil {
		_ = runtimeSet.Close(context.Background())
		return err
	}
	defer runtimeSet.Close(context.Background())

	prompt := scan.FormatAgentTaskPrompt(scannerArgs, intent)
	return console.RunTask(ctx, runtime, option, "scanner", "scanner", strings.Join(scannerArgs, " "), agentext.RunInput{Content: []*aop.Content{aop.Text(prompt)}})
}

func resolveScannerIntent(option *cfg.Option, store *skills.Store, command string) (string, error) {
	var sections []string
	if conceptURI := scan.ScannerConceptURI(command); conceptURI != "" && edition.Catalog().CLIAvailable(command) {
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
	intent, err = cfg.ApplySelectedSkills(intent, scan.FilterAutoSkill(option.Skills, command), store)
	if err != nil {
		return "", err
	}
	return strings.Join(append(sections, intent), "\n\n"), nil
}
