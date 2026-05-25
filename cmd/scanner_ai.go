package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/skills"
)

func runScannerAgentMode(ctx context.Context, option *Option, application *app.App, scannerArgs []string, logger telemetry.Logger) error {
	if application.Provider == nil {
		return fmt.Errorf("--ai requires a configured LLM provider")
	}

	// PID-file mutual exclusion: prevent stacking multiple agent processes
	// when loop scripts restart aiscan before the previous run has exited.
	pidFile := agentPIDFilePath()
	pidLock, err := acquireAgentPIDFile(pidFile, logger)
	if err != nil {
		return err
	}
	defer pidLock.Release()

	cmdReg := application.Commands

	command := scannerArgs[0]
	intent, err := resolveScannerAIIntent(option, application.Skills, command)
	if err != nil {
		return err
	}
	prompt := buildScannerAgentTaskPrompt(scannerArgs, intent)

	systemPrompt := agent.BuildSystemPrompt(&agent.PromptConfig{
		Tools:            cmdReg,
		ScannerDocs:      application.Commands.UsageDocs(),
		Skills:           application.Skills.Skills,
		ScannerAgentMode: true,
		ScannerName:      command,
	})

	logger.Debugf("system prompt length: %d chars", len(systemPrompt))
	output := newAgentOutput(option)
	output.Start("scanner", strings.Join(scannerArgs, " "))

	sess := newAgentSession(sessionConfig{
		Application: application,
		Option:      option,
		Logger:      logger,
	})
	defer sess.Cleanup()

	result, err := sess.Config.
		WithSystemPrompt(systemPrompt).
		WithStream(false).
		WithEventHandler(output.HandleEvent).
		Run(ctx, prompt)
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

	cfg := agent.Config{
		Provider:     application.Provider,
		Model:        option.Model,
		MaxTokens:    1600,
		SystemPrompt: scannerAIProcessSystemPrompt(command),
		Logger:       telemetry.NopLogger(),
	}
	result, err := cfg.Run(processCtx, buildScannerAIProcessPrompt(command, scannerArgs[1:], intent, output))
	if err != nil {
		return "", err
	}
	return result.Output, nil
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
	case "gogo", "spray", "katana", "zombie", "neutron", "passive", "scan":
		if !scannerCommandAvailable(command) {
			return ""
		}
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

// --- PID-file mutual exclusion ---

func agentPIDFilePath() string {
	return filepath.Join(os.TempDir(), "aiscan-agent.pid")
}

type agentPIDLock struct {
	path string
	file *os.File
	pid  int
}

// acquireAgentPIDFile checks whether another aiscan agent is already running.
// If a stale PID file exists (process dead), it is reclaimed.
func acquireAgentPIDFile(path string, logger telemetry.Logger) (*agentPIDLock, error) {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	pid := os.Getpid()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open agent pidfile %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if existingPID, readErr := readAgentPIDFile(path); readErr == nil && existingPID > 0 {
			return nil, fmt.Errorf("another aiscan agent is already running (PID %d, pidfile %s); kill it first or remove the pidfile", existingPID, path)
		}
		return nil, fmt.Errorf("another aiscan agent is already running (pidfile %s is locked)", path)
	}
	locked := true
	cleanup := func() {
		if locked {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		}
		_ = f.Close()
	}

	if info, statErr := f.Stat(); statErr == nil && info.Size() > 0 {
		if existingPID, readErr := readAgentPIDFile(path); readErr == nil && existingPID > 0 && existingPID != pid {
			if processExists(existingPID) {
				cleanup()
				return nil, fmt.Errorf("another aiscan agent is already running (PID %d, pidfile %s); kill it first or remove the pidfile", existingPID, path)
			}
			logger.Debugf("pidfile=%s stale_pid=%d action=reclaim", path, existingPID)
		} else if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			logger.Debugf("pidfile=%s action=rewrite reason=%q", path, readErr)
		}
	} else if statErr != nil {
		logger.Debugf("pidfile=%s action=rewrite reason=%q", path, statErr)
	}

	if err := f.Truncate(0); err != nil {
		cleanup()
		return nil, fmt.Errorf("truncate agent pidfile %s: %w", path, err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		cleanup()
		return nil, fmt.Errorf("seek agent pidfile %s: %w", path, err)
	}
	if _, err := fmt.Fprintf(f, "%d\n", pid); err != nil {
		cleanup()
		return nil, fmt.Errorf("write agent pidfile %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return nil, fmt.Errorf("sync agent pidfile %s: %w", path, err)
	}
	locked = false
	return &agentPIDLock{path: path, file: f, pid: pid}, nil
}

func (l *agentPIDLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = removeOwnedAgentPIDFile(l.path, l.pid)
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

func removeOwnedAgentPIDFile(path string, pid int) error {
	existingPID, err := readAgentPIDFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if existingPID != pid {
		return nil
	}
	return os.Remove(path)
}

func readAgentPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid %q", pidStr)
	}
	return pid, nil
}

func processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EPERM)
}
