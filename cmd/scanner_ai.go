package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tool"
	"github.com/chainreactors/aiscan/skills"
)

func runScannerAgentMode(ctx context.Context, option *Option, application *app.App, scannerArgs []string, logger telemetry.Logger) error {
	if application.Provider == nil {
		return fmt.Errorf("--ai requires a configured LLM provider")
	}

	toolReg := application.Tools
	if toolReg == nil {
		toolReg = tool.NewToolRegistry()
	}

	command := scannerArgs[0]
	intent, err := resolveScannerAIIntent(option, application.Skills, command)
	if err != nil {
		return err
	}
	prompt := buildScannerAgentTaskPrompt(scannerArgs, intent)

	systemPrompt := agent.BuildSystemPrompt(&agent.PromptConfig{
		Tools:            toolReg,
		ScannerDocs:      application.Commands.UsageDocs(),
		Skills:           application.Skills.Skills,
		ScannerAgentMode: true,
		ScannerName:      command,
	})

	logger.Debugf("system prompt length: %d chars", len(systemPrompt))
	output := newAgentOutput(option)
	output.Start("scanner", strings.Join(scannerArgs, " "))

	result, err := agent.RunWithEvents(ctx, prompt, toolReg, output.HandleEvent,
		agent.WithProvider(application.Provider),
		agent.WithSystemPrompt(systemPrompt),
		agent.WithModel(option.Model),
		agent.WithStream(false),
		agent.WithLogger(telemetry.NopLogger()),
	)
	if err != nil {
		return err
	}
	if result != nil && strings.TrimSpace(result.Output) != "" {
		output.Final(result.Output)
	}
	return nil
}

func buildScannerAgentTaskPrompt(scannerArgs []string, intent string) string {
	command := strings.Join(scannerArgs, " ")
	if strings.TrimSpace(intent) == "" {
		return fmt.Sprintf("Execute: %s", command)
	}
	return fmt.Sprintf("Execute: %s\n\nUser intent: %s", command, strings.TrimSpace(intent))
}

func runScannerAIProcess(ctx context.Context, option *Option, application *app.App, scannerArgs []string, output string, logger telemetry.Logger) (string, error) {
	if application.Provider == nil {
		return "", fmt.Errorf("--ai requires a configured LLM provider")
	}
	if len(scannerArgs) == 0 {
		return "", nil
	}
	command := scannerArgs[0]
	intent, err := resolveScannerAIIntent(option, application.Skills, command)
	if err != nil {
		return "", err
	}
	timeout := defaultInt(DefaultVerifyTimeout, 120)
	processCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	return agent.Run(processCtx, buildScannerAIProcessPrompt(command, scannerArgs[1:], intent, output), tool.NewToolRegistry(),
		agent.WithProvider(application.Provider),
		agent.WithModel(option.Model),
		agent.WithMaxTokens(1600),
		agent.WithSystemPrompt(scannerAIProcessSystemPrompt(command)),
		agent.WithLogger(telemetry.NopLogger()),
	)
}

func resolveScannerAIIntent(option *Option, store *skills.Store, command string) (string, error) {
	var sections []string
	if skill, ok := store.ByName(scannerAISkillName(command)); ok {
		sections = append(sections, skills.FormatInvocation(skill, ""))
	}

	intent := strings.TrimSpace(option.Prompt)
	if intent == "" && option.TaskFile != "" {
		data, err := os.ReadFile(option.TaskFile)
		if err != nil {
			return "", fmt.Errorf("read task file: %w", err)
		}
		intent = strings.TrimSpace(string(data))
	}
	if intent == "" {
		intent = defaultScannerAIIntent(command)
	}
	intent, err := applySelectedSkills(intent, scannerUserSkills(option.Skills, command), store)
	if err != nil {
		return "", err
	}
	sections = append(sections, intent)
	return strings.Join(sections, "\n\n"), nil
}

func scannerAISkillName(command string) string {
	switch command {
	case "gogo", "spray", "zombie", "neutron", "scan":
		return command
	default:
		return ""
	}
}

func scannerUserSkills(selected []string, command string) []string {
	auto := scannerAISkillName(command)
	if auto == "" {
		return selected
	}
	out := make([]string, 0, len(selected))
	for _, name := range selected {
		if strings.TrimSpace(name) == auto {
			continue
		}
		out = append(out, name)
	}
	return slices.Clip(out)
}

func defaultScannerAIIntent(command string) string {
	return "Process the scanner output according to the user's intent. If no specific intent is provided, briefly explain the important evidence in the output."
}

func buildScannerAIProcessPrompt(command string, args []string, intent, output string) string {
	return fmt.Sprintf(`User intent:
%s

Scanner command:
%s %s

Scanner output:
%s

Use the embedded scanner-output description to interpret the data, then follow the user intent.
`, strings.TrimSpace(intent), command, strings.Join(args, " "), scannerOutputForPrompt(output))
}

func scannerAIProcessSystemPrompt(command string) string {
	return "You are aiscan's scanner-output processor. Follow the supplied tool capability description and user intent. Use the scanner output as evidence and do not invent unsupported facts."
}

func scannerOutputForPrompt(output string) string {
	output = strings.TrimSpace(output)
	const maxPromptOutput = 60000
	if len(output) <= maxPromptOutput {
		return output
	}
	return output[:maxPromptOutput] + "\n... (scanner output truncated)"
}
