package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	skillpkg "github.com/chainreactors/aiscan/skills"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/reeflective/console"
	"github.com/spf13/cobra"
)

const agentPromptCommandName = "__prompt"

var errAgentConsoleExit = errors.New("agent console exit")

type agentConsole struct {
	ctx         context.Context
	option      *Option
	application *app.App
	session     *agent.Agent
	console     *console.Console
	menu        *console.Menu
	output      *agentOutput
}

func runInteractiveAgentMode(ctx context.Context, option *Option, logger telemetry.Logger) error {
	runtime, err := newAgentRuntime(ctx, option, logger)
	if err != nil {
		return err
	}
	defer runtime.application.Close()

	application := runtime.application
	if _, err := applySelectedSkills("", option.Skills, application.Skills); err != nil {
		return err
	}

	session := agent.New(application.Provider, application.Commands,
		agent.WithSystemPrompt(runtime.systemPrompt),
		agent.WithModel(option.Model),
		agent.WithStream(false),
		agent.WithLogger(telemetry.NopLogger()),
	)

	repl := newAgentConsole(ctx, option, application, session)
	return repl.start()
}

func newAgentConsole(ctx context.Context, option *Option, application *app.App, session *agent.Agent) *agentConsole {
	c := console.New("aiscan")
	c.NewlineAfter = true
	output := newAgentOutput(option)
	if session != nil {
		session.Subscribe(output.HandleEvent)
	}

	menu := c.NewMenu("agent")
	menu.Prompt().Primary = func() string { return "aiscan> " }
	menu.AddHistorySourceFile("history", agentConsoleHistoryPath())
	menu.ErrorHandler = func(err error) error {
		if errors.Is(err, errAgentConsoleExit) {
			return errAgentConsoleExit
		}
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return nil
	}

	repl := &agentConsole{
		ctx:         ctx,
		option:      option,
		application: application,
		session:     session,
		console:     c,
		menu:        menu,
		output:      output,
	}
	menu.SetCommands(repl.rootCommand)
	menu.Command = repl.rootCommand()
	c.SwitchMenu("agent")
	return repl
}

func (r *agentConsole) start() error {
	if r.output == nil || !r.output.quiet {
		fmt.Fprintln(os.Stderr, "aiscan interactive agent. Type /help for commands, /exit to quit.")
	}
	for {
		if err := r.ctx.Err(); err != nil {
			return err
		}

		line, err := r.console.Shell().Readline()
		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				fmt.Fprintln(os.Stdout)
				return nil
			case err.Error() == os.Interrupt.String():
				fmt.Fprintln(os.Stdout)
				continue
			default:
				fmt.Fprintf(os.Stderr, "error: read interactive input: %s\n", err)
				continue
			}
		}

		args, err := agentConsoleArgsForLine(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			continue
		}
		if len(args) == 0 {
			continue
		}

		if err := r.executeArgs(r.ctx, args); err != nil {
			if errors.Is(err, errAgentConsoleExit) {
				return nil
			}
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
		}
	}
}

func (r *agentConsole) executeArgs(ctx context.Context, args []string) error {
	root := r.rootCommand()
	root.SetArgs(args)
	root.SetContext(ctx)
	return root.Execute()
}

func (r *agentConsole) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "agent",
		Short:         "aiscan interactive agent",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.HiddenDefaultCmd = true
	root.SetHelpCommand(&cobra.Command{Use: "help", Hidden: true})
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)

	root.AddCommand(
		r.promptCommand(),
		r.helpCommand(root),
		r.resetCommand(),
		r.continueCommand(),
		r.exitCommand(),
	)
	root.AddCommand(r.ioaCommands()...)
	root.AddCommand(r.skillCommands()...)
	return root
}

func (r *agentConsole) promptCommand() *cobra.Command {
	return &cobra.Command{
		Use:    agentPromptCommandName,
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return r.runPrompt(cmd.Context(), args[0])
		},
	}
}

func (r *agentConsole) helpCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "/help",
		Short: "Show interactive commands",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return root.Help()
		},
	}
}

func (r *agentConsole) resetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "/reset",
		Short: "Clear conversation context",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			r.session.Reset()
			fmt.Fprintln(os.Stdout, "Context reset.")
		},
	}
}

func (r *agentConsole) continueCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "/continue",
		Short: "Continue without a new prompt",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r.ensureOutput().Start("continue", "")
			result, err := r.session.Continue(cmd.Context())
			if err != nil {
				return err
			}
			r.printResult(result)
			return nil
		},
	}
}

func (r *agentConsole) exitCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "/exit",
		Aliases: []string{"/quit"},
		Short:   "Exit",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return errAgentConsoleExit
		},
	}
}

func (r *agentConsole) skillCommands() []*cobra.Command {
	if r.application == nil || r.application.Skills == nil {
		return nil
	}
	commands := make([]*cobra.Command, 0, len(r.application.Skills.Skills))
	for _, skill := range r.application.Skills.Skills {
		skill := skill
		if strings.TrimSpace(skill.Name) == "" {
			continue
		}
		commands = append(commands, r.skillCommand(skill))
	}
	return commands
}

func (r *agentConsole) skillCommand(skill skillpkg.Skill) *cobra.Command {
	return &cobra.Command{
		Use:                "/" + skill.Name + " [prompt]",
		Short:              skill.Description,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return r.runSkill(cmd.Context(), skill, strings.Join(args, " "))
		},
	}
}

func (r *agentConsole) runPrompt(ctx context.Context, input string) error {
	prompt := skillpkg.ExpandCommand(input, r.application.Skills)
	prompt, err := applySelectedSkills(prompt, r.option.Skills, r.application.Skills)
	if err != nil {
		return err
	}
	r.ensureOutput().Start("prompt", input)
	result, err := r.session.Prompt(ctx, prompt)
	if err != nil {
		return err
	}
	r.printResult(result)
	return nil
}

func (r *agentConsole) runSkill(ctx context.Context, skill skillpkg.Skill, input string) error {
	prompt := skillpkg.FormatInvocation(skill, input)
	prompt, err := applySelectedSkills(prompt, r.option.Skills, r.application.Skills)
	if err != nil {
		return err
	}
	r.ensureOutput().Start("skill "+skill.Name, input)
	result, err := r.session.Prompt(ctx, prompt)
	if err != nil {
		return err
	}
	r.printResult(result)
	return nil
}

func (r *agentConsole) printResult(result *agent.Result) {
	if result == nil || strings.TrimSpace(result.Output) == "" {
		r.ensureOutput().Empty()
		return
	}
	r.ensureOutput().Final(result.Output)
}

func (r *agentConsole) ensureOutput() *agentOutput {
	if r.output == nil {
		r.output = newAgentOutput(r.option)
	}
	return r.output
}

func (r *agentConsole) ioaClient() (*ioaclient.Client, error) {
	ioaURL := r.option.IOAURL
	if ioaURL == "" {
		return nil, fmt.Errorf("IOA not configured: use --ioa-url")
	}
	return ioaclient.NewClient(ioaURL, "")
}

func (r *agentConsole) ioaCommands() []*cobra.Command {
	return []*cobra.Command{
		{
			Use:   "/spaces",
			Short: "List all IOA spaces",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				client, err := r.ioaClient()
				if err != nil {
					return err
				}
				return runIOASpaces(cmd.Context(), client, r.option)
			},
		},
		{
			Use:   "/messages <space>",
			Short: "List start messages in a space",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				client, err := r.ioaClient()
				if err != nil {
					return err
				}
				return runIOAMessages(cmd.Context(), client, r.option, ioaClientArgs{Space: args[0]})
			},
		},
		{
			Use:   "/context <space> <message-id>",
			Short: "View message thread/context",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				client, err := r.ioaClient()
				if err != nil {
					return err
				}
				return runIOAContext(cmd.Context(), client, r.option, ioaClientArgs{Space: args[0], MessageID: args[1]})
			},
		},
		{
			Use:   "/nodes [space]",
			Short: "List nodes (optionally scoped to a space)",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				client, err := r.ioaClient()
				if err != nil {
					return err
				}
				var a ioaClientArgs
				if len(args) > 0 {
					a.Space = args[0]
				}
				return runIOANodes(cmd.Context(), client, r.option, a)
			},
		},
	}
}

var ioaConsoleCommands = map[string]bool{
	"/spaces": true, "/messages": true, "/context": true, "/nodes": true,
}

func agentConsoleArgsForLine(line string) ([]string, error) {
	text := strings.TrimSpace(line)
	if text == "" {
		return nil, nil
	}
	if !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "/skill:") {
		return []string{agentPromptCommandName, text}, nil
	}
	command, rest, ok := strings.Cut(text, " ")
	if !ok {
		return []string{text}, nil
	}
	if ioaConsoleCommands[command] {
		result := []string{command}
		result = append(result, strings.Fields(rest)...)
		return result, nil
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
