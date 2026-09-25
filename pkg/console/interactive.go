package console

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/carapace-sh/carapace"
	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/eventbus"
	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	outputpkg "github.com/chainreactors/cyber/pkg/output"
	"github.com/chainreactors/tui/console"
	rlterm "github.com/chainreactors/tui/readline/terminal"
	"github.com/spf13/cobra"
)

const agentPromptCommandName = "__prompt"
const agentConsoleInterruptCommandName = "cyber-interrupt"
const agentConsoleCtrlCCommandName = "cyber-ctrl-c"
const agentConsoleToggleVerbosityCommandName = "cyber-toggle-verbosity"
const agentConsoleEscapeSequenceWait = 10 * time.Millisecond

// Some terminal applications leave focus reporting or Windows Terminal's
// Win32 input mode enabled. Readline does not consume those protocols; if they
// remain active, ordinary keys can arrive as strings such as
// "\x1b[191;53;47;1;0;1_" and leak into the editable line. Reset them at the
// application boundary before every read rather than teaching the shared
// readline package about an cyber-specific terminal lifecycle.
const agentConsoleResetInputModes = "\x1b[?1004l\x1b[?9001l"

var errAgentConsoleExit = errors.New("agent console exit")

type AgentConsole struct {
	ctx            context.Context
	option         *cfg.Option
	runtime        *agentsession.Runtime
	bindings       *consoleapi.Bindings
	session        *agentsession.Session
	console        *console.Console
	terminal       *rlterm.Terminal
	menu           *console.Menu
	output         *AgentOutput
	readlineBridge *readlineConsoleBridge
	stdout         io.Writer
	stderr         io.Writer
	// readlineActive is true only while the foreground goroutine is blocked in
	// Readline. Async agent output can then refresh the prompt without changing
	// the input buffer or creating a duplicate prompt between reads.
	readlineActive atomic.Bool
	// startupNotice, when set, is rendered once below the welcome banner (e.g.
	// an optional capability degradation warning). Set by the caller before Start.
	startupNotice string
	sessionDir    string

	inputID              string
	inputSeq             uint64
	workMu               sync.Mutex
	work                 sync.WaitGroup
	active               int
	closed               bool
	cancel               context.CancelFunc
	submitCtx            context.Context
	submitCancel         context.CancelFunc
	subscription         *eventbus.Subscription[*aop.Event]
	closeOnce            sync.Once
	previews             map[string]string
	compactContextTokens int
	compactContextWindow int
	pendingExit          atomic.Bool
}

func newAgentConsole(ctx context.Context, rt *agentsession.Runtime, session *agentsession.Session, option *cfg.Option, t *rlterm.Terminal, bindings *consoleapi.Bindings) *AgentConsole {
	if option == nil {
		option = &cfg.Option{}
	}
	ctx, cancel := context.WithCancel(ctx)
	submitCtx, submitCancel := context.WithCancel(ctx)
	if t == nil {
		t = rlterm.Local()
	}

	isTerminal := t.Control != nil && t.Control.IsTerminal()
	c := console.NewWithTerminal("aiscan", t)
	c.NewlineAfter = true
	configureAgentReadline(c)
	c.EnablePasteReferences(console.PasteReferenceConfig{Enabled: true})

	stdout := t.Out
	stderr := t.Err
	output := NewAgentOutputWithWriters(option, stdout, stderr, isTerminal)

	if stdout == nil {
		stdout = output.Stdout()
	}
	if stderr == nil {
		stderr = output.Stderr()
	}

	menu := c.NewMenu("agent")
	menu.ErrorHandler = func(err error) error {
		if errors.Is(err, errAgentConsoleExit) {
			return errAgentConsoleExit
		}
		fmt.Fprintf(stderr, "error: %s\n", err)
		return nil
	}

	repl := &AgentConsole{
		ctx:          ctx,
		option:       option,
		runtime:      rt,
		bindings:     bindings,
		session:      session,
		cancel:       cancel,
		submitCtx:    submitCtx,
		submitCancel: submitCancel,
		previews:     make(map[string]string),
		inputID:      aop.EnvelopeID(),
		console:      c,
		terminal:     t,
		menu:         menu,
		output:       output,
		stdout:       stdout,
		stderr:       stderr,
	}
	if isTerminal && isLocalAgentTerminal(t) && resolveRenderMode(renderModeValue(option)) == ModeInteractive {
		bridge := newReadlineConsoleBridge(c.Shell(), t.Out)
		output.SetReadlineMode(bridge)
		repl.readlineBridge = bridge
		c.Shell().OnReadlineReady = func() {
			bridge.SetReady(true)
		}
		c.Shell().OnReadlineDone = func() {
			bridge.SetReady(false)
		}
		repl.stdout = bridge
		repl.stderr = bridge
	}
	menu.Prompt().Primary = func() string {
		return agentComposerPrompt(output, repl.readlineBridge)
	}
	repl.workMu.Lock()
	repl.subscription = rt.Observe(repl.handleEvent)
	repl.workMu.Unlock()
	repl.configureCompletionKey()
	repl.configureInterruptKey()
	repl.configureCtrlCKey()
	repl.configureVerbosityToggleKey()
	menu.SetCommands(repl.rootCommand)
	menu.Command = repl.rootCommand()
	c.SwitchMenu("agent")
	return repl
}

func isLocalAgentTerminal(t *rlterm.Terminal) bool {
	if t == nil {
		return false
	}
	in, inOK := t.In.(*os.File)
	out, outOK := t.Out.(*os.File)
	return inOK && outOK && in == os.Stdin && out == os.Stdout
}

func (r *AgentConsole) Start() error {
	defer r.Close()
	history := agentConsoleHistoryPath(r.option)
	if err := os.MkdirAll(filepath.Dir(history), 0700); err != nil {
		return fmt.Errorf("create console history directory: %w", err)
	}
	r.menu.AddHistorySourceFile("history", history)
	r.runtime.Logger.SetOutput(r.stderr)
	if r.option.EvalCriteria != "" {
		if err := r.command("/eval " + r.option.EvalCriteria); err != nil {
			return err
		}
	}
	r.renderBanner()
	if r.fastInputEnabled() {
		return r.startFastInput()
	}
	return r.startReadline()
}

func (r *AgentConsole) startFastInput() error {
	reader := bufio.NewReader(r.terminal.In)
	for {
		if r.ctx.Err() != nil {
			return nil //nolint:nilerr // context cancellation is clean shutdown
		}

		r.promptCompactIfNeeded()

		fmt.Fprint(r.stderr, agentPromptString(r.ensureOutput()))
		r.setReadlineActive(true)
		line, err := readFastInputLine(r.ctx, reader)
		r.setReadlineActive(false)
		if err != nil && !errors.Is(err, io.EOF) {
			if errors.Is(err, context.Canceled) {
				fmt.Fprintln(r.stdout)
				return nil
			}
			fmt.Fprintf(r.stderr, "error: read interactive input: %s\n", err)
			continue
		}
		if errors.Is(err, io.EOF) && strings.TrimSpace(line) == "" {
			fmt.Fprintln(r.stdout)
			return nil
		}

		line = coalesceFastInput(line, reader)

		done, execErr := r.handleInputLine(line)
		if execErr != nil {
			if errors.Is(execErr, context.Canceled) && r.ctx.Err() != nil {
				fmt.Fprintln(r.stdout)
				return nil //nolint:nilerr // clean shutdown — intentionally swallow error on context cancel
			}
			fmt.Fprintf(r.stderr, "error: %s\n", execErr)
		}
		if done || errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func coalesceFastInput(firstLine string, reader *bufio.Reader) string {
	trimmed := strings.TrimSpace(firstLine)
	if trimmed == "" || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "!") {
		return firstLine
	}
	lines := []string{strings.TrimRight(firstLine, "\r\n")}
	for reader.Buffered() > 0 {
		extra, err := reader.ReadString('\n')
		extra = strings.TrimRight(extra, "\r\n")
		if extra != "" {
			lines = append(lines, extra)
		}
		if err != nil {
			break
		}
	}
	if len(lines) == 1 {
		return firstLine
	}
	return strings.Join(lines, "\n")
}

type fastInputResult struct {
	line string
	err  error
}

// readFastInputLine reads one line from reader, cancellable via ctx.
// NOTE: on context cancellation the blocked ReadString goroutine leaks
// until stdin is closed — Go blocking I/O has no cancellation mechanism.
func readFastInputLine(ctx context.Context, reader *bufio.Reader) (string, error) {
	resultCh := make(chan fastInputResult, 1)
	go func() {
		line, err := reader.ReadString('\n')
		resultCh <- fastInputResult{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-resultCh:
		return result.line, result.err
	}
}

func (r *AgentConsole) startReadline() error {
	for {
		if r.ctx.Err() != nil {
			return nil //nolint:nilerr // context cancellation is clean shutdown
		}

		r.promptCompactIfNeeded()

		r.setReadlineActive(true)
		r.resetTerminalInputModes()
		line, err := r.console.Readline()
		r.setReadlineActive(false)
		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				fmt.Fprintln(r.stdout)
				return nil
			case err.Error() == os.Interrupt.String():
				r.InterruptCurrentRun()
				continue
			default:
				fmt.Fprintf(r.stderr, "error: read interactive input: %s\n", err)
				continue
			}
		}

		r.pendingExit.Store(false)
		done, err := r.handleInputLine(line)
		if err != nil {
			if errors.Is(err, context.Canceled) && r.ctx.Err() != nil {
				fmt.Fprintln(r.stdout)
				return nil //nolint:nilerr // clean shutdown — intentionally swallow error on context cancel
			}
			fmt.Fprintf(r.stderr, "error: %s\n", err)
		}
		if done {
			return nil
		}
	}
}

func (r *AgentConsole) resetTerminalInputModes() {
	if r == nil || r.terminal == nil || r.terminal.Out == nil {
		return
	}
	if r.terminal.Control == nil || !r.terminal.Control.IsTerminal() {
		return
	}
	_, _ = io.WriteString(r.terminal.Out, agentConsoleResetInputModes)
}

func (r *AgentConsole) setReadlineActive(active bool) {
	if r == nil {
		return
	}
	r.readlineActive.Store(active)
	if r.readlineBridge != nil {
		r.readlineBridge.SetActive(active)
	}
	if !active && r.readlineBridge != nil {
		r.readlineBridge.SetReady(false)
	}
	if r.output != nil {
		r.output.SetInteractiveInputActive(active && !r.Running())
	}
}

func (r *AgentConsole) resolvePastedText(input string) (string, string) {
	if r == nil || r.console == nil || input == "" {
		return input, input
	}
	return input, r.console.ResolvePasteReferences(input)
}

func (r *AgentConsole) handleInputLine(line string) (bool, error) {
	if err := r.ctx.Err(); err != nil {
		return false, err
	}
	args, err := AgentConsoleArgsForLine(line)
	if err != nil || len(args) == 0 {
		return false, err
	}
	err = r.executeArgs(r.ctx, args)
	if errors.Is(err, errAgentConsoleExit) {
		return true, nil
	}
	return false, err
}

func agentPromptString(output *AgentOutput) string {
	if output != nil && output.color.Enabled {
		return output.color.Code(outputpkg.ANSIBold+outputpkg.ANSICyan) + "aiscan" +
			output.color.Code(outputpkg.ANSIReset) + " " + output.color.Dim("❯") + " "
	}
	return "aiscan> "
}

func agentComposerPrompt(output *AgentOutput, bridge *readlineConsoleBridge) string {
	prompt := agentPromptString(output)
	if bridge == nil {
		return prompt
	}
	if status := bridge.Status(); status != "" {
		return status + "\n" + prompt
	}
	return prompt
}

func (r *AgentConsole) fastInputEnabled() bool {
	mode := ""
	if r != nil && r.option != nil {
		mode = r.option.REPLMode
	}
	return fastInputEnabledForMode(mode)
}

func fastInputEnabledForMode(mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "rich", "readline", "console":
		return false
	case "fast", "plain", "simple":
		return true
	}
	return false
}

func (r *AgentConsole) executeArgs(ctx context.Context, args []string) error {
	root := r.rootCommand()
	root.SetArgs(args)
	root.SetContext(ctx)
	return root.Execute()
}

func (r *AgentConsole) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use: "agent", Short: "aiscan interactive agent",
		SilenceUsage: true, SilenceErrors: true,
	}
	root.CompletionOptions.HiddenDefaultCmd = true
	root.SetHelpCommand(&cobra.Command{Use: "help", Hidden: true})
	root.SetOut(r.stdout)
	root.SetErr(r.stderr)

	root.AddCommand(&cobra.Command{
		Use: agentPromptCommandName, Hidden: true, Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return r.submitPrompt(args[0], false)
		},
	})
	root.AddCommand(&cobra.Command{
		Use:                "!",
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return r.command("!" + args[0])
		},
	})
	for _, name := range r.pseudoCommandNames() {
		n := name
		root.AddCommand(&cobra.Command{
			Use:                "!" + n,
			Short:              n,
			DisableFlagParsing: true,
			RunE: func(c *cobra.Command, args []string) error {
				return r.command("!" + n + " " + strings.Join(args, " "))
			},
		})
	}

	for _, cmd := range r.allCommands() {
		root.AddCommand(cmd)
	}

	carapace.Gen(root).PositionalAnyCompletion(
		carapace.ActionCallback(func(c carapace.Context) carapace.Action {
			return r.atCompleteAction(c)
		}),
	)

	return root
}

func (r *AgentConsole) allCommands() []*cobra.Command {
	cmds := r.builtinCommands()
	cmds = append(cmds, r.skillCommands()...)
	cmds = append(cmds, r.providerCommands()...)
	if r.bindings != nil && r.bindings.Commands != nil {
		cmds = append(cmds, r.bindings.Commands(consoleapi.View{Out: r.stdout, Err: r.stderr, Table: r.printBoxTable,
			Command: r.command, RefreshStatus: func() { fmt.Fprint(r.stdout, r.renderStatus()) },
		})...)
	}
	return cmds
}

func (r *AgentConsole) builtinCommands() []*cobra.Command {
	cmds := []*cobra.Command{
		{Use: "/help", Short: "查看命令面板", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { fmt.Fprint(r.stdout, r.renderHelp()); return nil }},
		{Use: "/resume", Short: "恢复已保存会话 (/resume 选择，/resume <path|#index>)", DisableFlagParsing: true, RunE: func(_ *cobra.Command, args []string) error {
			raw := strings.TrimSpace(strings.Join(args, " "))
			if raw == "" && r.interactivePickerEnabled() {
				return r.resumeSessionInteractive()
			}
			if raw == "" || raw == "list" {
				text, err := r.renderSessions()
				if err == nil {
					fmt.Fprint(r.stdout, text)
				}
				return err
			}
			return r.resumeSession(raw)
		}},
		{Use: "/stop", Short: "停止本终端提交的当前和排队任务", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
			if !r.InterruptCurrentRun() {
				fmt.Fprintln(r.stderr, "No running task.")
			}
			return nil
		}},
		{Use: "/continue", Short: "继续当前会话", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return r.submitPrompt("", true) }},
		{Use: "/followup", Short: "排队到当前任务结束后再发送", DisableFlagParsing: true, Args: cobra.MinimumNArgs(1), RunE: func(_ *cobra.Command, args []string) error { return r.submitPrompt(strings.Join(args, " "), false) }},
		{Use: "/exit", Aliases: []string{"/quit"}, Short: "退出交互模式", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return errAgentConsoleExit }},
	}
	return cmds
}

func (r *AgentConsole) providerCommands() []*cobra.Command {
	return []*cobra.Command{
		{
			Use:                "/provider",
			Short:              "查看 LLM provider 配置",
			DisableFlagParsing: true,
			RunE: func(c *cobra.Command, args []string) error {
				fields := splitArgs(args)
				if len(fields) == 0 || (len(fields) == 1 && fields[0] == "list") {
					fmt.Fprint(r.stdout, r.renderProviders())
					return nil
				}
				return fmt.Errorf("provider configuration is changed through the Profile; use /provider to view it")
			},
		},
		{
			Use:                "/model",
			Short:              "查看/切换当前会话的模型",
			DisableFlagParsing: true,
			RunE: func(c *cobra.Command, args []string) error {
				ctx := c.Context()
				fields := splitArgs(args)
				if len(fields) == 0 {
					if r.interactivePickerEnabled() {
						return r.configureModelInteractive(ctx)
					}
					models, err := r.renderModels(ctx)
					if err != nil {
						return err
					}
					fmt.Fprint(r.stdout, models)
					return nil
				}
				if len(fields) == 1 && fields[0] == "list" {
					models, err := r.renderModels(ctx)
					if err != nil {
						return err
					}
					fmt.Fprint(r.stdout, models)
					return nil
				}
				switch fields[0] {
				case "set", "use":
					fields = fields[1:]
				}
				if len(fields) != 1 {
					return fmt.Errorf("usage: /model [list|<model>|#index]")
				}
				return r.configureModel(ctx, fields[0])
			},
		},
	}
}

func (r *AgentConsole) ensureOutput() *AgentOutput {
	if r.output == nil {
		r.output = NewAgentOutput(r.option)
	}
	return r.output
}

func (r *AgentConsole) refreshPromptAfterAsyncRun() {
	if r == nil || r.readlineBridge != nil || !r.readlineActive.Load() {
		return
	}
	if r.ctx != nil && r.ctx.Err() != nil {
		return
	}
	if r.output != nil && r.output.mode != ModeInteractive {
		return
	}
	if r.terminal == nil || r.terminal.Control == nil || !r.terminal.Control.IsTerminal() {
		return
	}
	if r.console == nil || r.console.Shell() == nil || r.console.Shell().Display == nil {
		return
	}
	r.console.Shell().RefreshWithoutAutocomplete()
}

func (r *AgentConsole) promptCompactIfNeeded() {
	c := r
	c.workMu.Lock()
	ctxTokens, ctxWindow := c.compactContextTokens, c.compactContextWindow
	c.compactContextTokens, c.compactContextWindow = 0, 0
	c.workMu.Unlock()
	if ctxTokens == 0 {
		return
	}

	fmt.Fprintf(r.stderr,
		"\n⚠ Context usage: %d%% (%dK/%dK tokens). Compact now? [y/N] ",
		ctxTokens*100/ctxWindow, ctxTokens/1000, ctxWindow/1000)

	answer := ""
	if r.terminal != nil && r.terminal.In != nil {
		line, _ := bufio.NewReader(r.terminal.In).ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(line))
	}
	if answer == "y" || answer == "yes" {
		if err := r.command("/compact"); err != nil {
			fmt.Fprintf(r.stderr, "Compact failed: %s\n", err)
		}
	}
}

func (r *AgentConsole) forceExit() {
	r.cancel()
	// Called on readline's key handling goroutine, so acceptance is serialized
	// with input processing and exits only this Console, never the host process.
	r.console.Shell().History.Accept(false, false, io.EOF)
}

func (r *AgentConsole) renderProviders() string {
	_, pc := r.runtime.ProviderState()
	if pc.Provider == "" {
		return "\n  No providers configured.\n\n"
	}
	rows := []helpRow{{Command: "#1  " + pc.Provider, Detail: pc.Model + "  ● active"}}
	for i, p := range r.runtime.ProviderFallbacks() {
		rows = append(rows, helpRow{Command: fmt.Sprintf("#%d  %s", i+2, p.Provider.Name()), Detail: p.Model + "  ○ configured"})
	}
	return r.renderPanel("providers", renderHelpRows(rows, r.output.color.Enabled), r.output.color.Enabled)
}

const modelListTimeout = 10 * time.Second

func (r *AgentConsole) resumeSession(path string) error {
	if r.Running() {
		return fmt.Errorf("cannot resume while a task is running")
	}
	if r.session == nil {
		return fmt.Errorf("agent session is not configured")
	}
	path, err := r.resolveSessionSelection(path)
	if err != nil {
		return err
	}

	messages, err := r.session.Resume(r.ctx, path)
	if err != nil {
		return err
	}
	fmt.Fprintf(r.stdout, "Resumed %d messages from %s\n", messages, path)
	return nil
}

func (r *AgentConsole) renderSessions() (string, error) {
	colorEnabled := r.output != nil && r.output.color.Enabled
	sessions, err := r.listSavedSessions()
	if err != nil {
		return "", err
	}
	if len(sessions) == 0 {
		return r.renderPanel("sessions", renderHelpRows([]helpRow{
			{Command: "sessions", Detail: "none saved"},
		}, colorEnabled), colorEnabled), nil
	}
	rows := make([]helpRow, 0, len(sessions))
	for i, session := range sessions {
		rows = append(rows, helpRow{
			Command: fmt.Sprintf("#%d", i+1),
			Detail:  filepath.Base(session.Path) + "  " + sessionDetail(session),
		})
	}
	return r.renderPanel("sessions", renderHelpRows(rows, colorEnabled), colorEnabled), nil
}

func (r *AgentConsole) listSavedSessions() ([]SavedSession, error) {
	dir := r.sessionDir
	if dir == "" {
		dir = filepath.Join(cfg.ResolveDataDir(r.option.DataDir), "sessions")
	}
	return listSavedSessions(dir)
}

func (r *AgentConsole) resumeSessionInteractive() error {
	if r.Running() {
		return fmt.Errorf("cannot resume while a task is running")
	}
	if r.session == nil {
		return fmt.Errorf("agent session is not configured")
	}
	sessions, err := r.listSavedSessions()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		rendered, err := r.renderSessions()
		if err != nil {
			return err
		}
		fmt.Fprint(r.stdout, rendered)
		return nil
	}

	choices := make([]choiceItem, 0, len(sessions))
	for _, session := range sessions {
		choices = append(choices, choiceItem{
			value: session.Path,
			title: filepath.Base(session.Path),
			desc:  sessionDetail(session),
		})
	}
	width, height := r.pickerSize()
	selected, ok, err := runChoicePicker("sessions", choices, "", width, height)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return r.resumeSession(selected)
}

func (r *AgentConsole) resolveSessionSelection(selector string) (string, error) {
	selector = strings.TrimSpace(strings.TrimPrefix(selector, "#"))
	if selector == "" {
		return "", fmt.Errorf("usage: /resume [list|<path>|#index]")
	}
	if strings.ContainsAny(selector, `/\`) || strings.EqualFold(filepath.Ext(selector), ".jsonl") {
		return selector, nil
	}
	if idx, err := strconv.Atoi(selector); err == nil {
		sessions, listErr := r.listSavedSessions()
		if listErr != nil {
			return "", listErr
		}
		if idx < 1 || idx > len(sessions) {
			return "", fmt.Errorf("session index out of range: %d", idx)
		}
		return sessions[idx-1].Path, nil
	}
	sessions, err := r.listSavedSessions()
	if err != nil {

		return "", err
	}
	for _, session := range sessions {
		if selector == session.Path || selector == filepath.Base(session.Path) {
			return session.Path, nil
		}
	}
	return selector, nil
}

func sessionDetail(session SavedSession) string {
	parts := make([]string, 0, 4)
	if ts := session.SortTime(); !ts.IsZero() {
		parts = append(parts, ts.Local().Format("2006-01-02 15:04:05"))
	}
	model := strings.TrimSpace(session.Model)
	if model != "" {
		parts = append(parts, model)
	}
	parts = append(parts, fmt.Sprintf("%d messages", session.Messages))
	return strings.Join(parts, "  ")
}

func (r *AgentConsole) renderModels(ctx context.Context) (string, error) {
	colorEnabled := r.output != nil && r.output.color.Enabled
	models, err := r.listProviderModels(ctx)
	if err != nil {
		return "", err
	}
	if len(models) == 0 {
		return r.renderPanel("models", renderHelpRows([]helpRow{
			{Command: "current", Detail: r.providerConfig().Provider + " / " + r.providerConfig().Model},
			{Command: "models", Detail: "none returned"},
		}, colorEnabled), colorEnabled), nil
	}

	current := strings.TrimSpace(r.providerConfig().Model)
	rows := []helpRow{
		{Command: "current", Detail: r.providerConfig().Provider + " / " + valueOrDash(current)},
	}
	for i, model := range models {
		command := fmt.Sprintf("#%d", i+1)
		detail := model
		if model == current {
			detail += "  active"
		}
		rows = append(rows, helpRow{Command: command, Detail: detail})
	}
	return r.renderPanel("models", renderHelpRows(rows, colorEnabled), colorEnabled), nil
}

func (r *AgentConsole) configureModel(ctx context.Context, selector string) error {
	if r.Running() {
		return fmt.Errorf("cannot change model while a task is running")
	}
	selector = strings.TrimSpace(strings.TrimPrefix(selector, "#"))
	if selector == "" {
		return fmt.Errorf("usage: /model [list|<model>|#index]")
	}
	models, err := r.listProviderModels(ctx)
	if err != nil {
		return err
	}
	model, err := resolveModelSelection(models, selector)
	if err != nil {
		return err
	}

	return r.applyModel(model)
}

func (r *AgentConsole) configureModelInteractive(ctx context.Context) error {
	if r.Running() {
		return fmt.Errorf("cannot change model while a task is running")
	}
	models, err := r.listProviderModels(ctx)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		rendered, err := r.renderModels(ctx)
		if err != nil {
			return err
		}
		fmt.Fprint(r.stdout, rendered)
		return nil
	}
	width, height := r.pickerSize()
	selected, ok, err := runModelPicker(models, r.providerConfig().Model, width, height)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return r.applyModel(selected)
}

func (r *AgentConsole) applyModel(model string) error {
	if err := r.session.SetModel(model); err != nil {
		return err
	}
	r.output.SetContextWindow(r.session.ContextWindow())
	pc := r.providerConfig()
	fmt.Fprintf(r.stdout, "Model ready: %s / %s\n", pc.Provider, pc.Model)
	return nil
}

func (r *AgentConsole) listProviderModels(ctx context.Context) ([]string, error) {
	pc := r.providerConfig()
	if strings.TrimSpace(pc.Provider) == "" && strings.TrimSpace(pc.BaseURL) == "" {
		return nil, fmt.Errorf("provider not configured")
	}
	req := &types.LLMProbeRequest{
		Provider: pc.Provider,
		BaseUrl:  pc.BaseURL,
		ApiKey:   pc.APIKey,
		Proxy:    pc.Proxy,
	}
	listCtx, cancel := context.WithTimeout(ctx, modelListTimeout)
	defer cancel()
	result, err := provider.ListLLMModels(listCtx, req, "")
	if err != nil {
		return nil, err
	}
	if !result.Ok {
		if strings.TrimSpace(result.Error) == "" {
			return nil, fmt.Errorf("list models failed")
		}
		return nil, fmt.Errorf("list models: %s", result.Error)
	}
	return result.Models, nil
}

func resolveModelSelection(models []string, selector string) (string, error) {
	if idx, err := strconv.Atoi(selector); err == nil {
		if idx < 1 || idx > len(models) {
			return "", fmt.Errorf("model index out of range: %d", idx)
		}
		return models[idx-1], nil
	}
	for _, model := range models {
		if model == selector {
			return model, nil
		}
	}
	for _, model := range models {
		if strings.EqualFold(model, selector) {
			return model, nil
		}
	}
	return "", fmt.Errorf("model %q is not in the provider model list", selector)
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func (r *AgentConsole) interactivePickerEnabled() bool {
	return r != nil &&
		r.terminal != nil &&
		r.terminal.Control != nil &&
		r.terminal.Control.IsTerminal() &&
		r.terminal.In == os.Stdin &&
		r.terminal.Out == os.Stdout
}

func (r *AgentConsole) pickerSize() (int, int) {
	width, height := 80, 18
	if r == nil || r.terminal == nil || r.terminal.Control == nil {
		return width, height
	}
	cols, rows := r.terminal.Control.Size()
	if cols > 0 {
		width = cols
	}
	if rows > 0 {
		height = rows - 4
	}
	if height < 10 {
		height = 10
	}
	if height > 24 {
		height = 24
	}
	return width, height
}

func (r *AgentConsole) pseudoCommandNames() []string {
	if r.runtime.CommandRegistry() == nil {
		return nil
	}
	return r.runtime.CommandRegistry().Names()
}

func splitArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	return strings.Fields(strings.Join(args, " "))
}

func AgentConsoleArgsForLine(line string) ([]string, error) {
	text := strings.TrimSpace(line)
	if text == "" {
		return nil, nil
	}
	if text == "/" {
		return []string{"/help"}, nil
	}
	if strings.HasPrefix(text, "!") {
		rest := strings.TrimSpace(text[1:])
		if rest == "" {
			return nil, nil
		}
		return []string{"!", rest}, nil
	}
	if !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "/skill:") {
		return []string{agentPromptCommandName, text}, nil
	}
	command, rest, ok := strings.Cut(text, " ")
	if !ok {
		return []string{text}, nil
	}
	return []string{command, strings.TrimSpace(rest)}, nil
}

func (r *AgentConsole) atCompleteAction(c carapace.Context) carapace.Action {
	if !strings.HasPrefix(c.Value, "@") {
		return carapace.ActionValues()
	}
	raw := c.Value[1:]
	fileAction := atFuzzyFileAction(raw)
	c.Value = raw
	nodeAction := r.extensionCompleteAction(c)
	return carapace.Batch(fileAction, nodeAction).ToA()
}

func (r *AgentConsole) extensionCompleteAction(c carapace.Context) carapace.Action {
	if r.bindings == nil || r.bindings.Complete == nil {
		return carapace.ActionValues()
	}
	ctx, cancel := context.WithTimeout(r.ctx, 3*time.Second)
	defer cancel()
	return carapace.ActionValues(r.bindings.Complete(ctx, c.Value)...).NoSpace()
}
func (r *AgentConsole) printBoxTable(title string, rows [][]string) {
	enabled := r.output != nil && r.output.color.Enabled
	fmt.Fprint(r.stdout, r.renderPanel(title, renderBoxTable(rows, enabled), enabled))
}

func agentConsoleHistoryPath(option *cfg.Option) string {
	var directory string
	if option != nil {
		directory = option.DataDir
	}
	return filepath.Join(cfg.ResolveDataDir(directory), "agent_history")
}

func (r *AgentConsole) providerConfig() agent.ProviderConfig {
	if r == nil || r.runtime == nil {
		return agent.ProviderConfig{}
	}
	_, pc := r.runtime.ProviderState()
	if r.session != nil {
		pc.Model = r.session.Model()
		pc.ContextWindow = r.session.ContextWindow()
	}
	return pc
}
