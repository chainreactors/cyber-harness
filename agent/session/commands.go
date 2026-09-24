package session

import (
	"context"
	"fmt"
	"strings"

	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/proto"
)

// Command binds immutable metadata to a session command. Handlers execute in
// the session queue and must not synchronously enqueue work on that same session.
// AdvertiseRemote affects discovery only, not protocol access control.
type Command struct {
	Spec            *types.CommandSpec
	Handler         func(context.Context, *Session, []string) (*types.CommandResult, error)
	AdvertiseRemote bool
	rotation        bool
}

func (c Command) invoke(ctx context.Context, session *Session, args []string) (result *types.CommandResult, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("agent command %s panicked: %v", c.Spec.Name, failure)
		}
	}()
	return c.Handler(ctx, session, args)
}

func commandDeclarations(extra []Command) ([]Command, map[string]Command, error) {
	values := append(builtinCommands(), extra...)
	index := make(map[string]Command)
	for i, value := range values {
		if value.Spec == nil || value.Handler == nil {
			return nil, nil, fmt.Errorf("agent command requires metadata and a handler")
		}
		value.Spec = proto.CloneOf(value.Spec)
		for _, name := range append([]string{value.Spec.Name}, value.Spec.Aliases...) {
			if !strings.HasPrefix(name, "/") || len(name) < 2 || strings.ContainsAny(name, " \t\r\n") {
				return nil, nil, fmt.Errorf("invalid agent command name %q", name)
			}
			if _, exists := index[name]; exists {
				return nil, nil, fmt.Errorf("duplicate agent command %q", name)
			}
			index[name] = value
		}
		values[i] = value
	}
	return values, index, nil
}

// CommandSpecs projects the same declarations used for dispatch. Returned
// protobufs are owned copies; callers cannot mutate the installed commands.
func (rt *Runtime) CommandSpecs(remote bool) []*types.CommandSpec {
	if rt == nil {
		return nil
	}
	var specs []*types.CommandSpec
	for _, value := range rt.commands {
		if !remote || value.AdvertiseRemote {
			specs = append(specs, proto.CloneOf(value.Spec))
		}
	}
	return specs
}

func builtinCommands() []Command {
	text := func(line, style, body string) (*types.CommandResult, error) {
		return commandText(line, style, body).result, nil
	}
	return []Command{
		{Spec: &types.CommandSpec{Name: "/help", Description: "查看运行时命令"}, Handler: func(_ context.Context, s *Session, _ []string) (*types.CommandResult, error) {
			var help strings.Builder
			help.WriteString("Runtime commands:\n")
			for _, spec := range s.baseState().runtime.CommandSpecs(false) {
				if spec.Name == "/help" {
					continue
				}
				usage := spec.Usage
				if usage == "" {
					usage = spec.Name
				}
				fmt.Fprintf(&help, "  %s\n", usage)
			}
			help.WriteString("  !<command>")
			return text("/help", CommandPresentationPreformatted, help.String())
		}},
		{Spec: &types.CommandSpec{Name: "/status", Description: "查看 Agent 的 LLM、工具和会话健康状态"}, AdvertiseRemote: true, Handler: func(_ context.Context, s *Session, _ []string) (*types.CommandResult, error) {
			return text("/status", CommandPresentationPreformatted, s.baseState().commands.statusText())
		}},
		{Spec: &types.CommandSpec{Name: "/clear", Description: "清空当前 Agent 上下文"}, AdvertiseRemote: true, rotation: true, Handler: func(ctx context.Context, s *Session, args []string) (*types.CommandResult, error) {
			return s.rotateCommand(ctx, coretool.JoinCommandLine("/clear", args))
		}},
		{Spec: &types.CommandSpec{Name: "/compact", Usage: "/compact [focus]", Description: "压缩当前 Agent 上下文"}, AdvertiseRemote: true, rotation: true, Handler: func(ctx context.Context, s *Session, args []string) (*types.CommandResult, error) {
			return s.rotateCommand(ctx, coretool.JoinCommandLine("/compact", args))
		}},
		{Spec: &types.CommandSpec{Name: "/eval", Aliases: []string{"/goal"}, Usage: "/eval [criteria|rounds <spec>|off]", Description: "运行时目标评估"}, Handler: func(_ context.Context, s *Session, args []string) (*types.CommandResult, error) {
			state := s.baseState().commands
			criteria := strings.TrimSpace(strings.Join(args, " "))
			line := coretool.JoinCommandLine("/eval", args)
			// "rounds" sets how long the loop may keep going: a number is a hard
			// ceiling, anything else is plain language the evaluator follows.
			if len(args) > 0 && strings.EqualFold(args[0], "rounds") {
				rounds := strings.TrimSpace(strings.Join(args[1:], " "))
				switch rounds {
				case "":
					if state.evalRounds == "" {
						return text(line, CommandPresentationPlain, "Eval rounds: auto (the evaluator decides when to stop)")
					}
					return text(line, CommandPresentationPlain, "Eval rounds: "+state.evalRounds)
				case "off", "auto":
					state.evalRounds = ""
					return text(line, CommandPresentationPlain, "Eval rounds: auto (the evaluator decides when to stop)")
				default:
					state.evalRounds = rounds
					return text(line, CommandPresentationPlain, "Eval rounds: "+rounds)
				}
			}
			switch criteria {
			case "":
				if state.evalCriteria == "" {
					return text(line, CommandPresentationPlain, "Goal evaluation: off")
				}
				status := "Goal evaluation: on\n  criteria: " + state.evalCriteria
				if state.evalRounds != "" {
					status += "\n  rounds: " + state.evalRounds
				}
				return text(line, CommandPresentationPlain, status)
			case "off":
				state.evalCriteria = ""
				return text(line, CommandPresentationPlain, "Goal evaluation disabled.")
			default:
				state.evalCriteria = criteria
				return text(line, CommandPresentationPlain, "Goal evaluation enabled: "+criteria)
			}
		}},
		{Spec: &types.CommandSpec{Name: "/loop", Usage: "/loop [interval prompt|list|stop name]", Description: "运行时定时循环"}, Handler: func(ctx context.Context, s *Session, args []string) (*types.CommandResult, error) {
			line := coretool.JoinCommandLine("/loop", args)
			if len(args) == 0 {
				args = []string{"list"}
			}
			result := s.baseState().commands.executeBash(ctx, line, "loop "+strings.Join(args, " "))
			return result.result, result.err
		}},
	}
}
