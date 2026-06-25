package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/carapace-sh/carapace"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	outputpkg "github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/reeflective/console"
	rlterm "github.com/reeflective/readline/terminal"
	"github.com/spf13/cobra"
)

const agentPromptCommandName = "__prompt"
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

	directMu     sync.Mutex
	directCancel context.CancelFunc
	pendingExit  atomic.Bool
}

func NewAgentConsole(ctx context.Context, option *cfg.Option, appInfo AppInfo, session *agent.Agent, output *AgentOutput, bus ...*eventbus.Bus[agent.Event]) *AgentConsole {
	return NewAgentConsoleWithTerminal(ctx, option, appInfo, session, output, nil, bus...)
}

func NewAgentConsoleWithTerminal(ctx context.Context, option *cfg.Option, appInfo AppInfo, session *agent.Agent, output *AgentOutput, t *rlterm.Terminal, bus ...*eventbus.Bus[agent.Event]) *AgentConsole {
	if t == nil {
		t = rlterm.Local()
	}
	c := console.NewWithTerminal("aiscan", t)
	c.NewlineAfter = true
	configureAgentReadline(c)
	stdout := t.Out
	stderr := t.Err
	if output == nil {
		if t.Control == nil {
			output = NewAgentOutput(option)
		} else {
			output = NewAgentOutputWithWriters(option, stdout, stderr, t.Control.IsTerminal())
		}
	}
	if stdout == nil {
		stdout = output.Stdout()
	}
	if stderr == nil {
		stderr = output.Stderr()
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
	}
	menu.Prompt().Primary = func() string {
		if repl.pendingExit.Load() {
			return ""
		}
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
	repl.configureInterruptKey()
	repl.configureCtrlCKey()
	repl.configureVerbosityToggleKey()
	menu.SetCommands(repl.rootCommand)
	menu.Command = repl.rootCommand()
	c.SwitchMenu("agent")
	return repl
}

func (r *AgentConsole) Start() error {
	r.renderBanner()
	defer r.stopController()
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

		fmt.Fprint(r.stderr, r.promptString())
		line, err := readFastInputLine(r.ctx, reader)
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

		r.readlineActive.Store(true)
		line, err := r.console.Shell().Readline()
		r.readlineActive.Store(false)
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
			Name: "/status", Description: "查看模型、渲染模式、IOA 和 skills",
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
	}
}

func (r *AgentConsole) ioaCommands() []Command {
	return []Command{
		{
			Name: "/spaces", Description: "List all IOA spaces",
			Args: ArgsNone,
			Run: func(ctx context.Context, _ *Session, _ []string) error {
				client, err := r.ioaClient()
				if err != nil {
					return err
				}
				return RunIOASpaces(ctx, client, r.option, r.stdout, r.stderr)
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
				return RunIOAMessages(ctx, client, r.option, cfg.IOAClientArgs{Space: args[0]}, r.stdout, r.stderr)
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
				var a cfg.IOAClientArgs
				if len(args) > 0 {
					a.Space = args[0]
				}
				return RunIOANodes(ctx, client, r.option, a, r.stdout, r.stderr)
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

func (r *AgentConsole) ioaClient() (*ioaclient.Client, error) {
	ioaURL := r.option.IOAURL
	if ioaURL == "" {
		return nil, fmt.Errorf("IOA not configured: use --ioa-url")
	}
	client, err := ioaclient.NewClient(ioaURL, "")
	if err != nil {
		return nil, err
	}
	if client.AccessKey() != "" {
		if err := client.EnsureRegistered(context.Background(), "aiscan-tui", "", nil); err != nil {
			return nil, fmt.Errorf("IOA auth: %w", err)
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

	resolved, err := agent.ResolveProvider(&pc)
	if err != nil {
		return err
	}
	prov, err := agent.NewProviderFromResolved(resolved)
	if err != nil {
		return err
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

	if resolved.Model != "" {
		fmt.Fprintf(r.stdout, "Provider ready: %s / %s\n", resolved.Provider, resolved.Model)
	} else {
		fmt.Fprintf(r.stdout, "Provider ready: %s\n", resolved.Provider)
	}
	return nil
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
		cmd, args, _ := strings.Cut(rest, " ")
		if args == "" {
			return []string{"!" + cmd}, nil
		}
		return []string{"!" + cmd, strings.TrimSpace(args)}, nil
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
	c.Value = c.Value[1:]
	return carapace.ActionFiles().Invoke(c).Prefix("@").ToA().NoSpace()
}

func agentConsoleHistoryPath() string {
	return filepath.Join(cfg.DataSubDir(""), "agent_history")
}
