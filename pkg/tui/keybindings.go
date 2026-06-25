package tui

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	outputpkg "github.com/chainreactors/aiscan/core/output"
	"github.com/reeflective/console"
	"github.com/reeflective/readline/inputrc"
)

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

func (r *AgentConsole) configureCtrlCKey() {
	if r == nil || r.console == nil || r.console.Shell() == nil {
		return
	}
	shell := r.console.Shell()
	shell.Keymap.Register(map[string]func(){
		agentConsoleCtrlCCommandName: func() {
			r.handleCtrlC()
		},
	})
	ctrlC := inputrc.Unescape(`\C-c`)
	for _, keymap := range []string{"emacs", "emacs-standard"} {
		_ = shell.Config.Bind(keymap, ctrlC, agentConsoleCtrlCCommandName, false)
	}
}

func (r *AgentConsole) handleCtrlC() {
	if r.InterruptCurrentRun() {
		return
	}
	if r.pendingExit.Load() {
		os.Exit(0)
	}
	r.pendingExit.Store(true)
	fmt.Fprintf(r.stderr, " Press Ctrl+C again to exit\n")
	go func() {
		time.Sleep(3 * time.Second)
		r.pendingExit.Store(false)
	}()
	shell := r.console.Shell()
	shell.Display.AcceptLine()
	shell.History.Accept(false, false, errors.New(os.Interrupt.String()))
}

func (r *AgentConsole) configureVerbosityToggleKey() {
	if r == nil || r.console == nil || r.console.Shell() == nil {
		return
	}
	shell := r.console.Shell()
	shell.Keymap.Register(map[string]func(){
		agentConsoleToggleVerbosityCommandName: func() {
			r.handleToggleVerbosity()
		},
	})
	ctrlO := inputrc.Unescape(`\C-o`)
	for _, keymap := range []string{"emacs", "emacs-standard"} {
		_ = shell.Config.Bind(keymap, ctrlO, agentConsoleToggleVerbosityCommandName, false)
	}
}

func (r *AgentConsole) handleToggleVerbosity() {
	out := r.ensureOutput()
	if out == nil {
		return
	}
	current := out.VerbosityLevel()
	next := (current + 1) % 3
	out.SetVerbosity(next)
	label := out.VerbosityLabel()
	if out.color.Enabled {
		fmt.Fprintf(r.stderr, "\n%s %s\n",
			out.dim("verbosity:"),
			out.color.Wrap(label, outputpkg.ANSICyan))
	} else {
		fmt.Fprintf(r.stderr, "\nverbosity: %s\n", label)
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
	r.InterruptCurrentRun()
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
