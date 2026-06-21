package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	outputpkg "github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/reeflective/console"
	"github.com/reeflective/readline/inputrc"
	rlterm "github.com/reeflective/readline/terminal"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const agentPromptCommandName = "__prompt"
const agentConsoleInterruptCommandName = "aiscan-interrupt"
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
		stdout = output.stdout
	}
	if stderr == nil {
		stderr = output.stderr
	}

	menu := c.NewMenu("agent")
	menu.Prompt().Primary = func() string {
		return agentPromptString(output)
	}
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
	if len(bus) > 0 && bus[0] != nil {
		repl.bus = bus[0]
	}
	if option != nil && option.EvalCriteria != "" {
		repl.evalCriteria = option.EvalCriteria
	}
	repl.controller = newInteractiveRunController(ctx, repl.agent, output)
	repl.controller.SetOnFinish(repl.refreshPromptAfterAsyncRun)
	repl.configureInterruptKey()
	menu.SetCommands(repl.rootCommand)
	menu.Command = repl.rootCommand()
	c.SwitchMenu("agent")
	return repl
}

func configureAgentReadline(c *console.Console) {
	if c == nil {
		return
	}
	shell := c.Shell()
	cfg := shell.Config
	_ = cfg.Set("autocomplete", true)
	_ = cfg.Set("usage-hint-always", false)
	_ = cfg.Set("history-autosuggest", true)
	_ = cfg.Set("show-all-if-ambiguous", true)
	_ = cfg.Set("show-all-if-unmodified", true)
	_ = cfg.Set("menu-complete-display-prefix", true)
	_ = cfg.Set("page-completions", false)
	_ = cfg.Set("completion-query-items", 1000)
	_ = cfg.Set("bell-style", "none")
	_ = cfg.Set("enable-bracketed-paste", false)
	// Bind Tab to menu-complete so arrow keys navigate the dropdown.
	for _, keymap := range []string{"emacs", "emacs-standard", "vi-insert"} {
		_ = cfg.Bind(keymap, `\t`, "menu-complete", false)
		_ = cfg.Bind(keymap, inputrc.Unescape(`\e[Z`), "menu-complete-backward", false)
	}
}

func (r *AgentConsole) configureInterruptKey() {
	if r == nil || r.console == nil || r.console.Shell() == nil {
		return
	}
	shell := r.console.Shell()
	shell.Keymap.Register(map[string]func(){
		agentConsoleInterruptCommandName: func() {
			r.handleEscapeInterruptKey()
		},
	})
	escape := inputrc.Unescape(`\e`)
	for _, keymap := range []string{"emacs", "emacs-standard"} {
		_ = shell.Config.Bind(keymap, escape, agentConsoleInterruptCommandName, false)
	}
}

func (r *AgentConsole) handleEscapeInterruptKey() {
	if r == nil || r.console == nil || r.console.Shell() == nil {
		return
	}
	shell := r.console.Shell()
	pending := string(shell.Keys.Read())
	if pending == "" {
		pending = readPendingTerminalBytes(agentConsoleEscapeSequenceWait)
	}
	keymap := string(shell.Keymap.Main())
	if feed, ok := agentConsoleEscapeSequenceFeed(shell.Config.Binds[keymap], pending); ok {
		shell.Keys.Feed(true, []rune(feed)...)
		return
	}
	if pending != "" {
		shell.Keys.Feed(true, []rune(pending)...)
		return
	}
	shell.Display.AcceptLine()
	shell.History.Accept(false, false, errors.New(os.Interrupt.String()))
}

func agentConsoleEscapeSequenceFeed(binds map[string]inputrc.Bind, pending string) (string, bool) {
	if len(binds) == 0 || pending == "" {
		return "", false
	}
	sequence := inputrc.Unescape(`\e`) + pending
	matches := make([]string, 0, 4)
	for seq := range binds {
		readlineSeq := agentConsoleReadlineSequence(seq)
		if len(readlineSeq) <= 1 || !strings.HasPrefix(readlineSeq, inputrc.Unescape(`\e`)) {
			continue
		}
		if strings.HasPrefix(sequence, readlineSeq) {
			matches = append(matches, seq)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		left := agentConsoleReadlineSequence(matches[i])
		right := agentConsoleReadlineSequence(matches[j])
		if len(left) == len(right) {
			return left < right
		}
		return len(left) > len(right)
	})
	for _, seq := range matches {
		bind := binds[seq]
		replacement, ok := agentConsoleEquivalentNonEscapeBind(binds, bind)
		if !ok {
			continue
		}
		return replacement + sequence[len(agentConsoleReadlineSequence(seq)):], true
	}
	return "", false
}

func agentConsoleEquivalentNonEscapeBind(binds map[string]inputrc.Bind, target inputrc.Bind) (string, bool) {
	if target.Action == "" {
		return "", false
	}
	candidates := make([]string, 0, 4)
	for seq, bind := range binds {
		if bind.Action != target.Action || bind.Macro != target.Macro || strings.HasPrefix(agentConsoleReadlineSequence(seq), inputrc.Unescape(`\e`)) {
			continue
		}
		candidates = append(candidates, seq)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if len(candidates[i]) == len(candidates[j]) {
			return candidates[i] < candidates[j]
		}
		return len(candidates[i]) < len(candidates[j])
	})
	if len(candidates) == 0 {
		return agentConsoleFallbackNonEscapeBind(target)
	}
	return candidates[0], true
}

func agentConsoleFallbackNonEscapeBind(target inputrc.Bind) (string, bool) {
	switch target.Action {
	case "previous-history", "history-search-backward":
		return inputrc.Unescape(`\C-p`), true
	case "next-history", "history-search-forward":
		return inputrc.Unescape(`\C-n`), true
	case "backward-char", "vi-backward-char":
		return inputrc.Unescape(`\C-b`), true
	case "forward-char", "vi-forward-char":
		return inputrc.Unescape(`\C-f`), true
	case "beginning-of-line":
		return inputrc.Unescape(`\C-a`), true
	case "end-of-line":
		return inputrc.Unescape(`\C-e`), true
	default:
		return "", false
	}
}

func agentConsoleReadlineSequence(seq string) string {
	if seq == "" {
		return ""
	}
	converted := make([]rune, 0, len(seq))
	for _, r := range seq {
		if inputrc.IsMeta(r) {
			converted = append(converted, inputrc.Esc, inputrc.Demeta(r))
			continue
		}
		converted = append(converted, r)
	}
	return string(converted)
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

// renderBanner prints a compact welcome block to stderr: title/version,
// resolved model, the session mode, and a short next-step hint. It uses fixed
// ANSI tokens so redirected or recorded sessions do not receive terminal
// background probes. stderr-TTY-only and skipped in quiet mode so redirected
// logs stay clean. Printed once into the scrollback (PTY-forward safe).
func (r *AgentConsole) renderBanner() {
	if r.output == nil || r.output.verbosity < 0 || r.output.stderr == nil {
		return
	}
	if !r.output.tty {
		return
	}
	fmt.Fprint(r.output.stderr, r.bannerOutput())
}

func (r *AgentConsole) bannerOutput() string {
	colorEnabled := r.output != nil && r.output.color.Enabled
	provider, model := r.providerModel()
	modelText := "not configured - run `aiscan --init`"
	modelStyle := ansiWarn
	switch {
	case provider != "" && model != "":
		modelText = provider + " / " + model
		modelStyle = ansiAccent
	case provider != "":
		modelText = provider
		modelStyle = ansiAccent
	}

	width := r.bannerWidth()
	header := ansiTitle("aiscan", colorEnabled) + " " + ansiDim("v"+cfg.Version, colorEnabled)

	var lines []string
	lines = append(lines, header)
	lines = append(lines, bannerKV("model", modelStyle(modelText, colorEnabled), colorEnabled))
	lines = append(lines, bannerKV("mode", ansiDim(r.sessionSummary(), colorEnabled), colorEnabled))
	lines = append(lines, bannerKV("help", renderInlineCommands([]string{"/help", "/status", "/exit"}, colorEnabled), colorEnabled))

	box := renderFixedBox(strings.Join(lines, "\n"), width, colorEnabled)
	intent := ansiDim("输入目标或任务即可；例如：扫描 192.168.1.10 的 Web 风险", colorEnabled)

	var b strings.Builder
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, box)
	fmt.Fprintln(&b, "  "+intent)
	if notice := strings.TrimSpace(r.startupNotice); notice != "" {
		fmt.Fprintln(&b, "  "+ansiWarn("⚠ "+notice, colorEnabled))
	}
	fmt.Fprintln(&b)
	return b.String()
}

func (r *AgentConsole) bannerWidth() int {
	const (
		minWidth     = 44
		defaultWidth = 64
		maxWidth     = 78
	)
	width := defaultWidth
	if r != nil && r.terminal != nil && r.terminal.Control != nil {
		if columns, _ := r.terminal.Control.Size(); columns > 0 {
			width = columns - 4
		}
	} else if r != nil && r.output != nil && r.output.stderr != nil {
		if columns := writerTerminalWidth(r.output.stderr); columns > 0 {
			width = columns - 4
		}
	}
	if width < minWidth {
		return minWidth
	}
	if width > maxWidth {
		return maxWidth
	}
	return width
}

func writerTerminalWidth(w io.Writer) int {
	file, ok := w.(*os.File)
	if !ok {
		return 0
	}
	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil || width <= 0 {
		return 0
	}
	return width
}

func bannerKV(label, value string, colorEnabled bool) string {
	return ansiDim(fmt.Sprintf("%-9s", label), colorEnabled) + value
}

func renderFixedBox(body string, width int, colorEnabled bool) string {
	const minInnerWidth = 16
	innerWidth := width - 4
	if innerWidth < minInnerWidth {
		innerWidth = minInnerWidth
	}
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if n := visibleRuneLen(line); n > innerWidth {
			innerWidth = n
		}
	}

	border := func(s string) string { return ansiDim(s, colorEnabled) }
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", border("╭"+strings.Repeat("─", innerWidth+2)+"╮"))
	for _, line := range lines {
		padding := innerWidth - visibleRuneLen(line)
		if padding < 0 {
			padding = 0
		}
		fmt.Fprintf(&b, "%s %s%s %s\n",
			border("│"),
			line,
			strings.Repeat(" ", padding),
			border("│"))
	}
	fmt.Fprint(&b, border("╰"+strings.Repeat("─", innerWidth+2)+"╯"))
	return b.String()
}

func visibleRuneLen(s string) int {
	return len([]rune(outputpkg.StripANSI(s)))
}

func ansiWrap(s, code string, enabled bool) string {
	if !enabled {
		return s
	}
	return code + s + outputpkg.ANSIReset
}

func ansiTitle(s string, enabled bool) string {
	return ansiWrap(s, outputpkg.ANSIBold+outputpkg.ANSICyan, enabled)
}

func ansiAccent(s string, enabled bool) string {
	return ansiWrap(s, outputpkg.ANSICyan, enabled)
}

func ansiWarn(s string, enabled bool) string {
	return ansiWrap(s, outputpkg.ANSIYellow, enabled)
}

func ansiDim(s string, enabled bool) string {
	return ansiWrap(s, "\033[90m", enabled)
}

func renderInlineCommands(commands []string, colorEnabled bool) string {
	parts := make([]string, 0, len(commands))
	for _, command := range commands {
		parts = append(parts, ansiAccent(command, colorEnabled))
	}
	return strings.Join(parts, ansiDim("  ", colorEnabled))
}

func (r *AgentConsole) sessionSummary() string {
	var parts []string
	if r != nil && r.output != nil {
		switch r.output.mode {
		case ModeForwarded:
			parts = append(parts, "forwarded")
		default:
			parts = append(parts, "pty")
		}
		if r.output.stream {
			parts = append(parts, "stream")
		} else if r.output.markdown {
			parts = append(parts, "pretty")
		} else {
			parts = append(parts, "plain")
		}
	}
	if r != nil && r.option != nil {
		if space := strings.TrimSpace(r.option.Space); space != "" {
			parts = append(parts, "space "+space)
		}
	}
	if len(parts) == 0 {
		return "pty"
	}
	return strings.Join(parts, " · ")
}

func (r *AgentConsole) providerModel() (string, string) {
	if r.appInfo.Commands == nil {
		return "", ""
	}
	pc := r.appInfo.ProviderConfig
	return pc.Provider, pc.Model
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
			Name: "/reset", Description: "清空当前会话上下文",
			Args: ArgsNone,
			Run: func(_ context.Context, s *Session, _ []string) error {
				if s.Controller != nil && s.Controller.Running() {
					return fmt.Errorf("task is running — use /stop first")
				}
				s.Agent.Reset()
				fmt.Fprintln(r.stdout, "Context reset.")
				return nil
			},
		},
		{
			Name: "/continue", Description: "不追加输入，继续上一轮任务",
			Args: ArgsNone,
			Run: func(_ context.Context, s *Session, _ []string) error {
				return s.Controller.Continue()
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
			Name: "/eval", Description: "设置/查看/关闭 goal evaluation (/eval off 关闭)",
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

// ---------------------------------------------------------------------------
// Rendering: help, status, panels
// ---------------------------------------------------------------------------

func (r *AgentConsole) renderHelp() string {
	colorEnabled := r.output != nil && r.output.color.Enabled
	cmds := r.allCommands()
	rows := make([]helpRow, 0, len(cmds)+3)
	for _, c := range cmds {
		if c.Hidden {
			continue
		}
		rows = append(rows, helpRow{Command: c.Name, Detail: c.Description})
	}
	rows = append(rows, helpRow{})
	rows = append(rows, helpRow{Command: "普通文本", Detail: "直接发送自然语言任务"})
	rows = append(rows, helpRow{Command: "! <命令>", Detail: "直接执行 bash/伪命令（跳过 LLM）"})
	return r.renderPanel("commands", renderHelpRows(rows, colorEnabled), colorEnabled)
}

func (r *AgentConsole) renderStatus() string {
	colorEnabled := r.output != nil && r.output.color.Enabled
	info := CollectStatus(r.replSession(), r.sessionSummary(), agentConsoleHistoryPath())
	rows := []helpRow{
		{Command: "model", Detail: info.Provider + " / " + info.Model},
		{Command: "render", Detail: info.Mode},
		{Command: "task", Detail: info.Task},
		{Command: "ioa", Detail: info.IOA},
		{Command: "history", Detail: info.History},
	}
	if info.Skills != "" {
		rows = append(rows, helpRow{Command: "skills", Detail: info.Skills})
	}
	return r.renderPanel("status", renderHelpRows(rows, colorEnabled), colorEnabled)
}

type helpRow struct {
	Command string
	Detail  string
}

const helpRowCommandWidth = 18

func renderHelpRows(rows []helpRow, colorEnabled bool) string {
	var b strings.Builder
	for _, row := range rows {
		if row.Command == "" && row.Detail == "" {
			b.WriteByte('\n')
			continue
		}
		command := ansiAccent(fmt.Sprintf("%-*s", helpRowCommandWidth, row.Command), colorEnabled)
		detail := ansiDim(row.Detail, colorEnabled)
		fmt.Fprintf(&b, "%s%s\n", command, detail)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r *AgentConsole) renderPanel(title, body string, colorEnabled bool) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "aiscan"
	}
	header := ansiTitle(title, colorEnabled)
	return "\n" + renderFixedBox(header+"\n"+body, r.bannerWidth(), colorEnabled) + "\n\n"
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

func agentConsoleHistoryPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(configDir) == "" {
		return ".aiscan_agent_history"
	}
	dir := filepath.Join(configDir, "aiscan")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ".aiscan_agent_history"
	}
	return filepath.Join(dir, "agent_history")
}
