package tui

import (
	"bufio"
	"context"
	"encoding/json"
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
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	outputpkg "github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/probe"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/tui/console"
	rlterm "github.com/chainreactors/tui/readline/terminal"
	"github.com/spf13/cobra"
)

const agentPromptCommandName = "__prompt"
const agentConsoleCompleteCommandName = "aiscan-complete"
const agentConsoleInterruptCommandName = "aiscan-interrupt"
const agentConsoleCtrlCCommandName = "aiscan-ctrl-c"
const agentConsoleToggleVerbosityCommandName = "aiscan-toggle-verbosity"
const agentConsoleEscapeSequenceWait = 10 * time.Millisecond

var errAgentConsoleExit = errors.New("agent console exit")

type AgentConsole struct {
	ctx        context.Context
	option     *cfg.Option
	appInfo    AppInfo
	agent      *agent.Agent
	console    *console.Console
	terminal   *rlterm.Terminal
	menu       *console.Menu
	output     *AgentOutput
	stdout     io.Writer
	stderr     io.Writer
	controller *interactiveRunController
	bus        *eventbus.Bus[agent.Event]
	// readlineActive is true only while the foreground goroutine is blocked in
	// Readline. Async agent output can then refresh the prompt without changing
	// the input buffer or creating a duplicate prompt between reads.
	readlineActive atomic.Bool
	// startupNotice, when set, is rendered once below the welcome banner (e.g.
	// an IOA-unavailable degradation warning). Set by the caller before Start.
	startupNotice string
	evalCriteria  string
	sessionDir    string

	directMu     sync.Mutex
	directCancel context.CancelFunc
	pendingExit  atomic.Bool
	onExit       func()

	split *SplitTerminal
}

func NewAgentConsole(ctx context.Context, option *cfg.Option, appInfo AppInfo, session *agent.Agent, output *AgentOutput, bus ...*eventbus.Bus[agent.Event]) *AgentConsole {
	return NewAgentConsoleWithTerminal(ctx, option, appInfo, session, output, nil, bus...)
}

func NewAgentConsoleWithTerminal(ctx context.Context, option *cfg.Option, appInfo AppInfo, session *agent.Agent, output *AgentOutput, t *rlterm.Terminal, bus ...*eventbus.Bus[agent.Event]) *AgentConsole {
	if t == nil {
		t = rlterm.Local()
	}

	// Determine whether to activate the split-pane layout. When enabled,
	// readline uses an input-area writer (serialized via the same mutex)
	// while all agent output routes to the upper scroll region.
	var split *SplitTerminal
	isTerminal := t.Control != nil && t.Control.IsTerminal()
	useSplit := isTerminal && splitEnabled(int(os.Stdout.Fd()), resolveRenderMode())

	var consoleTerminal *rlterm.Terminal
	if useSplit {
		split = NewSplitTerminal(t.Out, int(os.Stdout.Fd()))
		// Readline gets a terminal whose Out/Err go through the split
		// input writer so that prompt rendering is serialized with output.
		consoleTerminal = rlterm.Stream(t.In, split.InputWriter(), split.InputWriter(), t.Control)
	} else {
		consoleTerminal = t
	}

	c := console.NewWithTerminal("aiscan", consoleTerminal)
	if useSplit {
		c.NewlineAfter = false
	} else {
		c.NewlineAfter = true
	}
	configureAgentReadline(c)
	c.EnablePasteReferences(console.PasteReferenceConfig{Enabled: true})

	stdout := t.Out
	stderr := t.Err
	if output == nil {
		if t.Control == nil {
			output = NewAgentOutput(option)
		} else {
			output = NewAgentOutputWithWriters(option, stdout, stderr, isTerminal)
		}
	}

	// In split mode, redirect AgentOutput into the scroll region and
	// point console stdout/stderr there too.
	if split != nil {
		output.SetSplitMode(split)
		stdout = split.OutputWriter()
		stderr = split.OutputWriter()
	} else {
		if stdout == nil {
			stdout = output.Stdout()
		}
		if stderr == nil {
			stderr = output.Stderr()
		}
	}

	menu := c.NewMenu("agent")
	menu.AddHistorySourceFile("history", agentConsoleHistoryPath())
	menu.ErrorHandler = func(err error) error {
		if errors.Is(err, errAgentConsoleExit) {
			return errAgentConsoleExit
		}
		fmt.Fprintf(stderr, "error: %s\n", err)
		return nil
	}

	repl := &AgentConsole{
		ctx:      ctx,
		option:   option,
		appInfo:  appInfo,
		agent:    session,
		console:  c,
		terminal: t,
		menu:     menu,
		output:   output,
		stdout:   stdout,
		stderr:   stderr,
		split:    split,
	}
	menu.Prompt().Primary = func() string {
		return agentPromptString(output)
	}
	if len(bus) > 0 && bus[0] != nil {
		repl.bus = bus[0]
	}
	if option != nil && option.EvalCriteria != "" {
		repl.evalCriteria = option.EvalCriteria
	}
	repl.controller = newInteractiveRunController(ctx, repl.agent, output)
	repl.controller.SetOnFinish(repl.refreshPromptAfterAsyncRun)
	repl.configureCompletionKey()
	repl.configureInterruptKey()
	repl.configureCtrlCKey()
	repl.configureVerbosityToggleKey()
	menu.SetCommands(repl.rootCommand)
	menu.Command = repl.rootCommand()
	c.SwitchMenu("agent")
	return repl
}

// NewAgentConsoleWithWriters builds a non-interactive console that executes
// individual REPL lines against the same command implementation as the TUI.
func NewAgentConsoleWithWriters(ctx context.Context, option *cfg.Option, appInfo AppInfo, session *agent.Agent, stdout, stderr io.Writer, bus ...*eventbus.Bus[agent.Event]) *AgentConsole {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = stdout
	}
	control := rlterm.NewControl(false, 80, 24)
	terminal := rlterm.Stream(strings.NewReader(""), stdout, stderr, control)
	output := NewStaticAgentOutputWithWriters(option, stdout, stderr, false)
	return NewAgentConsoleWithTerminal(ctx, option, appInfo, session, output, terminal, bus...)
}

// ExecuteLineAndWait runs one REPL input line and waits for any async agent run
// started by that line. It is used by the web chat bridge so slash and bang
// commands do not drift from the interactive console behavior.
func (r *AgentConsole) ExecuteLineAndWait(line string) (bool, error) {
	done, err := r.handleInputLine(line)
	if r.controller != nil {
		r.controller.Wait()
	}
	return done, err
}

func (r *AgentConsole) Start() error {
	if r.split != nil {
		r.split.Setup()
		r.activateSplitLogger()
		defer r.split.Teardown()
	}
	r.renderBanner()
	defer r.stopController()
	if r.fastInputEnabled() {
		return r.startFastInput()
	}
	return r.startReadline()
}

func (r *AgentConsole) activateSplitLogger() {
	if r == nil || r.split == nil {
		return
	}
	splitLogger := telemetry.GlobalLogger(telemetry.LogConfig{
		Debug:  r.option != nil && r.option.Debug,
		Quiet:  r.option != nil && r.option.Quiet,
		Output: r.stderr,
		Color:  r.option == nil || !r.option.NoColor,
	})
	if r.appInfo.OnLoggerChange != nil {
		r.appInfo.OnLoggerChange(splitLogger)
	}
	if r.agent != nil {
		r.agent.SetLogger(splitLogger)
	}
	if r.appInfo.Commands != nil {
		r.appInfo.Commands.SetLogger(splitLogger)
	}
}

func (r *AgentConsole) startFastInput() error {
	reader := bufio.NewReader(r.terminal.In)
	for {
		if r.ctx.Err() != nil {
			return nil //nolint:nilerr // context cancellation is clean shutdown
		}

		r.promptCompactIfNeeded()

		fmt.Fprint(r.stderr, r.promptString())
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

		if r.split != nil {
			r.split.PrepareInputArea()
		}

		r.setReadlineActive(true)
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

func (r *AgentConsole) setReadlineActive(active bool) {
	if r == nil {
		return
	}
	r.readlineActive.Store(active)
	if r.output != nil {
		r.output.SetInteractiveInputActive(active && (r.controller == nil || !r.controller.Running()))
	}
}

func (r *AgentConsole) resolvePastedText(input string) (string, string) {
	if r == nil || r.console == nil || input == "" {
		return input, input
	}
	return input, r.console.ResolvePasteReferences(input)
}

func (r *AgentConsole) handleInputLine(line string) (bool, error) {
	args, err := AgentConsoleArgsForLine(line)
	if err != nil {
		return false, err
	}
	if len(args) == 0 {
		return false, nil
	}

	if err := r.executeArgs(r.ctx, args); err != nil {
		if errors.Is(err, errAgentConsoleExit) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func (r *AgentConsole) promptString() string {
	return agentPromptString(r.ensureOutput())
}

func agentPromptString(output *AgentOutput) string {
	if output != nil && output.color.Enabled {
		return output.color.Code(outputpkg.ANSIBold+outputpkg.ANSICyan) + "aiscan" +
			output.color.Code(outputpkg.ANSIReset) + " " + output.color.Dim("❯") + " "
	}
	return "aiscan> "
}

func (r *AgentConsole) fastInputEnabled() bool {
	isTerminal := false
	if r != nil && r.terminal != nil && r.terminal.Control != nil {
		isTerminal = r.terminal.Control.IsTerminal()
	}
	return fastInputEnabledForMode(os.Getenv("AISCAN_REPL"), isTerminal)
}

func fastInputEnabledForMode(mode string, _ bool) bool {
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

func (r *AgentConsole) replSession() *Session {
	s := &Session{
		Ctx:          r.ctx,
		Option:       r.option,
		AppInfo:      r.appInfo,
		Agent:        r.agent,
		Controller:   r.ensureController(),
		EvalCriteria: r.evalCriteria,
		ResolveInput: r.resolvePastedText,
	}
	s.OnEvalChange = func(criteria string) {
		r.evalCriteria = criteria
		r.syncEvalToController()
	}
	return s
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
			return RunPrompt(r.replSession(), "prompt", args[0])
		},
	})
	root.AddCommand(&cobra.Command{
		Use:                "!",
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return r.executeBashDirect(c.Context(), args[0])
		},
	})
	for _, name := range r.pseudoCommandNames() {
		n := name
		root.AddCommand(&cobra.Command{
			Use:                "!" + n,
			Short:              n,
			DisableFlagParsing: true,
			RunE: func(c *cobra.Command, args []string) error {
				return r.executeBashDirect(c.Context(), n+" "+strings.Join(args, " "))
			},
		})
	}

	for _, cmd := range r.allCommands() {
		root.AddCommand(wrapCommand(cmd, r.replSession()))
	}

	carapace.Gen(root).PositionalAnyCompletion(
		carapace.ActionCallback(func(c carapace.Context) carapace.Action {
			return r.atCompleteAction(c)
		}),
	)

	return root
}

func (r *AgentConsole) allCommands() []Command {
	s := r.replSession()
	var cmds []Command
	cmds = append(cmds, r.builtinCommands()...)
	cmds = append(cmds, SkillCommands(s)...)
	cmds = append(cmds, r.providerCommands()...)
	cmds = append(cmds, r.ioaCommands()...)
	return cmds
}

// StaticCommands returns the non-skill REPL commands (builtin + provider + IOA).
// Safe to call on a zero-value receiver — the returned Command.Run closures are
// unusable, but the metadata (Name, Aliases, Description, Hidden) is correct.
func (r *AgentConsole) StaticCommands() []Command {
	var cmds []Command
	cmds = append(cmds, r.builtinCommands()...)
	cmds = append(cmds, r.providerCommands()...)
	cmds = append(cmds, r.ioaCommands()...)
	return cmds
}

func (r *AgentConsole) builtinCommands() []Command {
	return []Command{
		{
			Name: "/help", Description: "查看命令面板",
			Args: ArgsNone,
			Run: func(_ context.Context, _ *Session, _ []string) error {
				fmt.Fprint(r.stdout, r.renderHelp())
				return nil
			},
		},
		{
			Name: "/status", Description: "查看模型、渲染模式、Server 和 skills",
			Args: ArgsNone,
			Run: func(_ context.Context, _ *Session, _ []string) error {
				fmt.Fprint(r.stdout, r.renderStatus())
				return nil
			},
		},
		{
			Name: "/clear", Description: "清空当前会话上下文",
			Args: ArgsNone,
			Run: func(_ context.Context, s *Session, _ []string) error {
				if s.Controller != nil && s.Controller.Running() {
					return fmt.Errorf("task is running — use /stop first")
				}
				s.Agent.Reset()
				fmt.Fprintln(r.stdout, "Context cleared.")
				return nil
			},
		},
		{
			Name: "/resume", Description: "恢复已保存会话 (/resume 选择，/resume <path|#index>)",
			Args: ArgsOptional,
			Run: func(_ context.Context, _ *Session, args []string) error {
				raw := strings.TrimSpace(strings.Join(args, " "))
				if raw == "" {
					if r.interactivePickerEnabled() {
						return r.resumeSessionInteractive()
					}
					sessions, err := r.renderSessions()
					if err != nil {
						return err
					}
					fmt.Fprint(r.stdout, sessions)
					return nil
				}
				if raw == "list" {
					sessions, err := r.renderSessions()
					if err != nil {
						return err
					}
					fmt.Fprint(r.stdout, sessions)
					return nil
				}
				return r.resumeSession(raw)
			},
		},
		{
			Name: "/stop", Description: "停止当前正在运行的任务",
			Args: ArgsNone,
			Run: func(_ context.Context, _ *Session, _ []string) error {
				if !r.InterruptCurrentRun() {
					fmt.Fprintln(r.stderr, "No running task.")
				}
				return nil
			},
		},
		{
			Name: "/continue", Description: "继续当前会话",
			Args: ArgsNone,
			Run: func(_ context.Context, s *Session, _ []string) error {
				if s.Controller == nil {
					return fmt.Errorf("agent controller is not configured")
				}
				return s.Controller.Continue()
			},
		},
		{
			Name: "/followup", Description: "排队到当前任务结束后再发送",
			Args: ArgsExact1,
			Run: func(ctx context.Context, s *Session, args []string) error {
				return RunPrompt(s, "follow-up", args[0])
			},
		},
		{
			Name: "/eval", Aliases: []string{"/goal"}, Description: "设置/查看/关闭 goal evaluation (/eval off 关闭)",
			Args: ArgsOptional,
			Run: func(_ context.Context, s *Session, args []string) error {
				text := strings.TrimSpace(strings.Join(args, " "))
				switch text {
				case "":
					if s.EvalCriteria == "" {
						fmt.Fprintln(r.stdout, "Goal evaluation: off")
					} else {
						fmt.Fprintf(r.stdout, "Goal evaluation: on\n  criteria: %s\n", s.EvalCriteria)
					}
				case "off":
					s.EvalCriteria = ""
					if s.OnEvalChange != nil {
						s.OnEvalChange("")
					}
					fmt.Fprintln(r.stdout, "Goal evaluation disabled.")
				default:
					s.EvalCriteria = text
					if s.OnEvalChange != nil {
						s.OnEvalChange(text)
					}
					fmt.Fprintf(r.stdout, "Goal evaluation enabled: %s\n", text)
				}
				return nil
			},
		},
		{
			Name: "/compact", Description: "压缩当前会话上下文 (/compact [focus instructions])",
			Args: ArgsOptional,
			Run: func(ctx context.Context, s *Session, args []string) error {
				if s.Controller != nil && s.Controller.Running() {
					return fmt.Errorf("task is running — use /stop first")
				}
				if len(s.Agent.MessagesSnapshot()) < 4 {
					fmt.Fprintln(r.stdout, "Nothing to compact (too few messages).")
					return nil
				}
				instructions := strings.TrimSpace(strings.Join(args, " "))
				result, err := s.Agent.Compact(ctx, agent.CompactConfig{
					CustomInstructions: instructions,
				})
				if err != nil {
					return err
				}
				fmt.Fprintf(r.stdout, "Compacted: ~%d → ~%d tokens (%d messages kept)\n",
					result.TokensBefore, result.TokensAfter, result.KeptMessages)
				return nil
			},
		},
		{
			Name: "/loop", Description: "定时循环任务 (/loop 30s <prompt> | /loop list | /loop stop <name>)",
			Args: ArgsOptional,
			Run: func(ctx context.Context, s *Session, args []string) error {
				cmd, ok := s.AppInfo.Commands.Get("loop")
				if !ok {
					return fmt.Errorf("loop command not registered")
				}
				if len(args) == 0 {
					args = []string{"list"}
				}
				return cmd.Execute(ctx, args)
			},
		},
		{
			Name: "/exit", Aliases: []string{"/quit"}, Description: "退出交互模式",
			Args: ArgsNone,
			Run: func(_ context.Context, _ *Session, _ []string) error {
				return errAgentConsoleExit
			},
		},
	}
}

func (r *AgentConsole) providerCommands() []Command {
	return []Command{
		{
			Name:        "/provider",
			Description: "查看/管理 LLM provider 链",
			Args:        ArgsOptional,
			Run: func(_ context.Context, _ *Session, args []string) error {
				fields := splitArgs(args)
				if len(fields) == 0 || (len(fields) == 1 && fields[0] == "list") {
					fmt.Fprint(r.stdout, r.renderProviders())
					return nil
				}
				switch fields[0] {
				case "set", "use":
					return r.configureProvider(fields[1:])
				default:
					fmt.Fprintf(r.stderr, "unknown subcommand: %s (use: list, set)\n", fields[0])
				}
				return nil
			},
		},
		{
			Name:        "/model",
			Description: "查看/切换当前 provider 的模型",
			Args:        ArgsOptional,
			Run: func(ctx context.Context, _ *Session, args []string) error {
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

func (r *AgentConsole) ioaCommands() []Command {
	return []Command{
		{
			Name: "/spaces", Description: "List all spaces",
			Args: ArgsNone,
			Run: func(ctx context.Context, _ *Session, _ []string) error {
				client, err := r.ioaClient()
				if err != nil {
					return err
				}
				return r.renderIOASpaces(ctx, client)
			},
		},
		{
			Name: "/messages", Description: "List start messages in a space",
			Args: ArgsExact1,
			Run: func(ctx context.Context, _ *Session, args []string) error {
				client, err := r.ioaClient()
				if err != nil {
					return err
				}
				return r.renderIOAMessages(ctx, client, args[0])
			},
		},
		{
			Name: "/context", Description: "View message thread/context",
			Args: ArgsOptional,
			Run: func(ctx context.Context, _ *Session, args []string) error {
				fields := splitArgs(args)
				if len(fields) < 2 {
					return fmt.Errorf("usage: /context <space> <message-id>")
				}
				client, err := r.ioaClient()
				if err != nil {
					return err
				}
				return RunIOAContext(ctx, client, r.option, cfg.IOAClientArgs{Space: fields[0], MessageID: fields[1]}, r.stdout, r.stderr)
			},
		},
		{
			Name: "/nodes", Description: "List nodes (optionally scoped to a space)",
			Args: ArgsOptional,
			Run: func(ctx context.Context, _ *Session, args []string) error {
				client, err := r.ioaClient()
				if err != nil {
					return err
				}
				space := ""
				if len(args) > 0 {
					space = args[0]
				}
				return r.renderIOANodes(ctx, client, space)
			},
		},
	}
}

// wrapCommand converts a Command into a cobra.Command. No special-case logic —
// every Command's Run is self-contained.
func wrapCommand(cmd Command, s *Session) *cobra.Command {
	cc := &cobra.Command{
		Use:   cmd.Name,
		Short: cmd.Description,
	}
	if len(cmd.Aliases) > 0 {
		cc.Aliases = cmd.Aliases
	}
	cc.Hidden = cmd.Hidden
	switch cmd.Args {
	case ArgsNone:
		cc.Args = cobra.NoArgs
	case ArgsExact1:
		cc.Args = cobra.ExactArgs(1)
		cc.DisableFlagParsing = true
	case ArgsOptional:
		cc.DisableFlagParsing = true
	}
	if cmd.Run != nil {
		run := cmd.Run
		cc.RunE = func(c *cobra.Command, args []string) error {
			return run(c.Context(), s, args)
		}
	}
	return cc
}

func (r *AgentConsole) ensureOutput() *AgentOutput {
	if r.output == nil {
		r.output = NewAgentOutput(r.option)
	}
	return r.output
}

func (r *AgentConsole) ensureController() *interactiveRunController {
	if r.controller == nil {
		r.controller = newInteractiveRunController(r.ctx, r.agent, r.ensureOutput())
		r.controller.SetOnFinish(r.refreshPromptAfterAsyncRun)
	}
	r.syncEvalToController()
	return r.controller
}

func (r *AgentConsole) syncEvalToController() {
	if r.controller == nil {
		return
	}
	if r.evalCriteria == "" {
		r.controller.Eval = nil
		return
	}
	model := ""
	if r.option != nil {
		model = r.option.EvalModel
	}
	if model == "" && r.appInfo.Commands != nil {
		model = r.appInfo.ProviderConfig.Model
	}
	var prov agent.Provider
	if r.appInfo.Commands != nil {
		prov = r.appInfo.Provider
	}
	r.controller.Eval = &EvalSettings{
		Criteria: r.evalCriteria,
		Model:    model,
		Provider: prov,
		Bus:      r.bus,
	}
}

func (r *AgentConsole) refreshPromptAfterAsyncRun() {
	if r == nil || !r.readlineActive.Load() {
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
	r.console.Shell().Refresh()
}

func (r *AgentConsole) promptCompactIfNeeded() {
	c := r.controller
	if c == nil {
		return
	}
	c.mu.Lock()
	ctxTokens, ctxWindow := c.compactContextTokens, c.compactContextWindow
	c.compactContextTokens, c.compactContextWindow = 0, 0
	c.mu.Unlock()
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
		result, err := r.agent.Compact(r.ctx, agent.CompactConfig{})
		if err != nil {
			fmt.Fprintf(r.stderr, "Compact failed: %s\n", err)
		} else {
			fmt.Fprintf(r.stderr, "Compacted: ~%d → ~%d tokens (%d messages kept)\n",
				result.TokensBefore, result.TokensAfter, result.KeptMessages)
		}
	}
}

func (r *AgentConsole) setDirectCancel(fn context.CancelFunc) {
	r.directMu.Lock()
	r.directCancel = fn
	r.directMu.Unlock()
}

// InterruptCurrentRun stops the current agent run or direct command.
func (r *AgentConsole) InterruptCurrentRun() bool {
	if r.controller != nil && r.controller.Stop() {
		r.ensureOutput().Stopping()
		return true
	}
	r.directMu.Lock()
	cancel := r.directCancel
	r.directMu.Unlock()
	if cancel != nil {
		cancel()
		return true
	}
	return false
}

func (r *AgentConsole) stopController() {
	if r.controller != nil {
		r.controller.StopAndWait()
	}
}

func (r *AgentConsole) SetOnExit(fn func()) {
	r.onExit = fn
}

func (r *AgentConsole) forceExit() {
	r.stopController()
	if r.onExit != nil {
		r.onExit()
	}
	os.Exit(0)
}

func (r *AgentConsole) ioaClient() (*ioaclient.Client, error) {
	ioaURL := r.option.IOAURL
	if ioaURL == "" {
		return nil, fmt.Errorf("server not configured: use --server-url")
	}
	client, err := ioaclient.NewClient(ioaURL, "")
	if err != nil {
		return nil, err
	}
	if client.AccessKey() != "" {
		if err := client.EnsureRegistered(context.Background(), "aiscan-tui", "", nil); err != nil {
			return nil, fmt.Errorf("server auth: %w", err)
		}
	}
	return client, nil
}

func (r *AgentConsole) renderProviders() string {
	colorEnabled := r.output != nil && r.output.color.Enabled
	info := CollectStatus(r.replSession(), "", "")
	if len(info.Providers) == 0 {
		return "\n  No providers configured.\n\n"
	}
	var rows []helpRow
	for i, p := range info.Providers {
		status := "○ standby"
		if p.Active {
			status = "● active"
		}
		label := fmt.Sprintf("#%d  %s", i+1, p.Name)
		detail := fmt.Sprintf("%-24s %s", p.Model, status)
		rows = append(rows, helpRow{Command: label, Detail: detail})
	}
	return r.renderPanel("providers", renderHelpRows(rows, colorEnabled), colorEnabled)
}

func (r *AgentConsole) configureProvider(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: /provider set --provider openai --base-url <url> --api-key <key> --model <model>")
	}
	if r.controller != nil && r.controller.Running() {
		return fmt.Errorf("cannot change provider while a task is running")
	}

	pc := r.appInfo.ProviderConfig
	for i := 0; i < len(args); i++ {
		key := args[i]
		value := ""
		if k, v, ok := strings.Cut(key, "="); ok {
			key, value = k, v
		} else {
			if i+1 >= len(args) {
				return fmt.Errorf("%s requires a value", key)
			}
			i++
			value = args[i]
		}
		value = strings.TrimSpace(value)
		switch strings.TrimLeft(key, "-") {
		case "provider":
			pc.Provider = value
		case "base-url", "base_url":
			pc.BaseURL = value
		case "api-key", "api_key":
			pc.APIKey = value
		case "model":
			pc.Model = value
		case "proxy":
			pc.Proxy = value
		default:
			return fmt.Errorf("unknown provider option: %s", key)
		}
	}

	resolved, err := r.applyProviderConfig(pc)
	if err != nil {
		return err
	}
	if resolved.Model != "" {
		fmt.Fprintf(r.stdout, "Provider ready: %s / %s\n", resolved.Provider, resolved.Model)
	} else {
		fmt.Fprintf(r.stdout, "Provider ready: %s\n", resolved.Provider)
	}
	return nil
}

const modelListTimeout = 10 * time.Second

func (r *AgentConsole) resumeSession(path string) error {
	if r.controller != nil && r.controller.Running() {
		return fmt.Errorf("cannot resume while a task is running")
	}
	if r.agent == nil {
		return fmt.Errorf("agent session is not configured")
	}
	path, err := r.resolveSessionSelection(path)
	if err != nil {
		return err
	}
	data, err := agent.LoadSession(path)
	if err != nil {
		return err
	}
	r.agent.LoadMessages(data.Messages)
	fmt.Fprintf(r.stdout, "Resumed %d messages from %s\n", len(data.Messages), path)
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

func (r *AgentConsole) listSavedSessions() ([]agent.SessionInfo, error) {
	dir := r.sessionDir
	if dir == "" {
		dir = cfg.DataSubDir("sessions")
	}
	return agent.ListSessions(dir)
}

func (r *AgentConsole) resumeSessionInteractive() error {
	if r.controller != nil && r.controller.Running() {
		return fmt.Errorf("cannot resume while a task is running")
	}
	if r.agent == nil {
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
	sessions, err := r.listSavedSessions()
	if err != nil {
		return "", err
	}
	if idx, err := strconv.Atoi(selector); err == nil {
		if idx < 1 || idx > len(sessions) {
			return "", fmt.Errorf("session index out of range: %d", idx)
		}
		return sessions[idx-1].Path, nil
	}
	for _, session := range sessions {
		if selector == session.Path || selector == filepath.Base(session.Path) {
			return session.Path, nil
		}
	}
	return selector, nil
}

func sessionDetail(session agent.SessionInfo) string {
	parts := make([]string, 0, 4)
	if ts := session.SortTime(); !ts.IsZero() {
		parts = append(parts, ts.Local().Format("2006-01-02 15:04:05"))
	}
	model := strings.Trim(strings.TrimSpace(session.Provider)+"/"+strings.TrimSpace(session.Model), "/")
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
			{Command: "current", Detail: r.appInfo.ProviderConfig.Provider + " / " + r.appInfo.ProviderConfig.Model},
			{Command: "models", Detail: "none returned"},
		}, colorEnabled), colorEnabled), nil
	}

	current := strings.TrimSpace(r.appInfo.ProviderConfig.Model)
	rows := []helpRow{
		{Command: "current", Detail: r.appInfo.ProviderConfig.Provider + " / " + valueOrDash(current)},
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
	if r.controller != nil && r.controller.Running() {
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
	if r.controller != nil && r.controller.Running() {
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
	selected, ok, err := runModelPicker(models, r.appInfo.ProviderConfig.Model, width, height)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return r.applyModel(selected)
}

func (r *AgentConsole) applyModel(model string) error {
	pc := r.appInfo.ProviderConfig
	pc.Model = model
	resolved, err := r.applyProviderConfig(pc)
	if err != nil {
		return err
	}
	fmt.Fprintf(r.stdout, "Model ready: %s / %s\n", resolved.Provider, resolved.Model)
	return nil
}

func (r *AgentConsole) listProviderModels(ctx context.Context) ([]string, error) {
	pc := r.appInfo.ProviderConfig
	if strings.TrimSpace(pc.Provider) == "" && strings.TrimSpace(pc.BaseURL) == "" {
		return nil, fmt.Errorf("provider not configured")
	}
	req := probe.LLMProbeRequest{
		Provider: pc.Provider,
		BaseURL:  pc.BaseURL,
		APIKey:   pc.APIKey,
		Proxy:    pc.Proxy,
	}
	listCtx, cancel := context.WithTimeout(ctx, modelListTimeout)
	defer cancel()
	result, err := probe.ListLLMModels(listCtx, req, "")
	if err != nil {
		return nil, err
	}
	if !result.OK {
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

func (r *AgentConsole) applyProviderConfig(pc agent.ProviderConfig) (agent.ProviderConfig, error) {
	if pc.Model != r.appInfo.ProviderConfig.Model {
		pc.Images = nil
	}
	resolved, err := agent.ResolveProvider(&pc)
	if err != nil {
		return agent.ProviderConfig{}, err
	}
	prov, err := agent.NewProviderFromResolved(resolved)
	if err != nil {
		return agent.ProviderConfig{}, err
	}

	r.appInfo.Provider = prov
	r.appInfo.ProviderConfig = *resolved
	if r.appInfo.OnProviderChange != nil {
		r.appInfo.OnProviderChange(prov, *resolved)
	}
	if r.agent != nil {
		r.agent.Cfg.Provider = prov
		r.agent.Cfg.Model = resolved.Model
	}
	if r.option != nil {
		cfg.ApplyResolvedProviderOptions(r.option, *resolved)
		r.option.LLMProxy = resolved.Proxy
	}
	r.syncEvalToController()

	return *resolved, nil
}

func (r *AgentConsole) pseudoCommandNames() []string {
	if r.appInfo.Commands == nil {
		return nil
	}
	return r.appInfo.Commands.Names()
}

// executeBashDirect runs a command line directly through the command registry,
// bypassing the LLM agent. Pseudo-commands (gogo, cyberhub, etc.) and shell
// commands are both supported, matching the "! command" REPL prefix.
func (r *AgentConsole) executeBashDirect(ctx context.Context, cmdLine string) error {
	reg := r.appInfo.Commands
	if reg == nil {
		return fmt.Errorf("command registry not available")
	}
	directCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.setDirectCancel(cancel)
	defer r.setDirectCancel(nil)

	if tool, ok := reg.GetTool("bash"); ok {
		payload, err := json.Marshal(map[string]string{"command": cmdLine})
		if err != nil {
			return err
		}
		result, err := tool.Execute(directCtx, string(payload))
		if err != nil {
			return err
		}
		if text := result.Text(); text != "" {
			fmt.Fprint(r.stdout, text)
		}
		return nil
	}

	result, err := reg.Execute(directCtx, cmdLine)
	if err != nil {
		if errors.Is(err, context.Canceled) && directCtx.Err() != nil && ctx.Err() == nil {
			fmt.Fprintln(r.stderr, "\ncommand interrupted")
			return nil
		}
		return err
	}
	if result != "" {
		fmt.Fprint(r.stdout, result)
	}
	return nil
}

// splitArgs splits a single-element args slice (from DisableFlagParsing) into fields.
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
	nodeAction := r.atNodeCompleteAction(c)
	return carapace.Batch(fileAction, nodeAction).ToA()
}

func (r *AgentConsole) atNodeCompleteAction(c carapace.Context) carapace.Action {
	if r.option == nil || r.option.IOAURL == "" {
		return carapace.ActionValues()
	}
	client, err := r.ioaClient()
	if err != nil {
		return carapace.ActionValues()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if r.option.Space != "" {
		space, err := client.ResolveSpace(ctx, r.option.Space)
		if err == nil {
			var names []string
			for _, n := range space.Nodes {
				names = append(names, "@"+n.Name)
			}
			return carapace.ActionValues(names...).NoSpace()
		}
	}
	nodes, err := client.ListNodes(ctx)
	if err != nil {
		return carapace.ActionValues()
	}
	var names []string
	for _, n := range nodes {
		names = append(names, "@"+n.Name)
	}
	return carapace.ActionValues(names...).NoSpace()
}

func agentConsoleHistoryPath() string {
	return filepath.Join(cfg.DataSubDir(""), "agent_history")
}
