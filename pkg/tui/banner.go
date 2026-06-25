package tui

import (
	"fmt"
	"io"
	"os"
	"strings"

	cfg "github.com/chainreactors/aiscan/core/config"
	outputpkg "github.com/chainreactors/aiscan/core/output"
	"golang.org/x/term"
)

// renderBanner prints a compact welcome block to stderr: title/version,
// resolved model, the session mode, and a short next-step hint. It uses fixed
// ANSI tokens so redirected or recorded sessions do not receive terminal
// background probes. stderr-TTY-only and skipped in quiet mode so redirected
// logs stay clean. Printed once into the scrollback (PTY-forward safe).
func (r *AgentConsole) renderBanner() {
	if r.output == nil || r.output.VerbosityLevel() < 0 || r.output.Stderr() == nil {
		return
	}
	if !r.output.tty {
		return
	}
	fmt.Fprint(r.output.Stderr(), r.bannerOutput())
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
	lines = append(lines, bannerKV("keys", ansiDim("Esc", colorEnabled)+" "+ansiDim("interrupt", colorEnabled)+ansiDim("  ", colorEnabled)+ansiDim("Ctrl+O", colorEnabled)+" "+ansiDim("verbosity", colorEnabled), colorEnabled))

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
	} else if r != nil && r.output != nil && r.output.Stderr() != nil {
		if columns := writerTerminalWidth(r.output.Stderr()); columns > 0 {
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
	return outputpkg.NewColor(enabled).Wrap(s, code)
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
	return ansiWrap(s, outputpkg.ANSIDim, enabled)
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
		case ModeStatic:
			parts = append(parts, "static")
		case ModeForwarded:
			parts = append(parts, "forwarded")
		default:
			parts = append(parts, "pty")
		}
		if r.output.stream.enabled {
			parts = append(parts, "stream")
		} else if r.output.Markdown() {
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
