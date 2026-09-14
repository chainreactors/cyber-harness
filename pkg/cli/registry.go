// Package cli collects inert declarations before argument parsing.
package cli

import (
	"context"
	"fmt"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	flags "github.com/jessevdk/go-flags"
	"io"
	"reflect"
	"strings"
)

type Environment struct {
	Config   *cfg.Option
	Logger   telemetry.Logger
	Out, Err io.Writer
}
type Action struct {
	Run        func(context.Context, Environment) error
	Persistent bool
}
type binding struct {
	key     string
	command *flags.Command
	group   *flags.Group
}
type Registry struct {
	Parser         *flags.Parser
	actions        map[*flags.Command]Action
	bindings       []binding
	sources        map[*flags.Option]string
	commandSources map[*flags.Command]string
	sealed         bool
}

func New(parser *flags.Parser) *Registry {
	return &Registry{Parser: parser, actions: map[*flags.Command]Action{}, sources: map[*flags.Option]string{}, commandSources: map[*flags.Command]string{}}
}
func (r *Registry) command(path string) *flags.Command {
	cmd := r.Parser.Command
	for _, part := range strings.Fields(path) {
		cmd = cmd.Find(part)
		if cmd == nil {
			return nil
		}
	}
	return cmd
}
func (r *Registry) Group(source, path, key string, group cfg.FlagGroup) error {
	if err := r.writable(source); err != nil {
		return err
	}
	cmd := r.command(path)
	if cmd == nil {
		return fmt.Errorf("unknown command %q", path)
	}
	// Parse and validate in an isolated group before touching the live parser.
	probe := flags.NewParser(nil, flags.None)
	candidate, err := probe.AddGroup(group.Name, group.Description, group.Options)
	if err != nil {
		return err
	}
	if err := r.validateOptions(cmd.Name, groupOptions(cmd.Group), groupOptions(candidate), source); err != nil {
		return err
	}
	added, err := cmd.AddGroup(group.Name, group.Description, group.Options)
	if err != nil {
		return err
	}
	for _, option := range groupOptions(added) {
		r.sources[option] = source
	}
	r.bindings = append(r.bindings, binding{key: key, group: added, command: cmd})
	return r.Validate()
}
func (r *Registry) Command(source, path, description string, data any, action Action) error {
	if err := r.writable(source); err != nil {
		return err
	}
	parts := strings.Fields(path)
	if len(parts) == 0 {
		return fmt.Errorf("command path is required")
	}
	probe := flags.NewParser(nil, flags.None)
	candidate, err := probe.AddCommand(parts[len(parts)-1], description, "", data)
	if err != nil {
		return err
	}
	if err := r.validateOptions(path, nil, groupOptions(candidate.Group), source); err != nil {
		return err
	}
	parent := r.Parser.Command
	for _, part := range parts[:len(parts)-1] {
		child := parent.Find(part)
		if child == nil {
			var err error
			child, err = parent.AddCommand(part, part, "", &struct{}{})
			if err != nil {
				return err
			}
		}
		if r.commandSources[child] == "" {
			r.commandSources[child] = source
		}
		parent = child
	}
	name := parts[len(parts)-1]
	if previous := parent.Find(name); previous != nil {
		return fmt.Errorf("duplicate command %q (sources %s and %s)", path, r.commandSources[previous], source)
	}
	cmd, err := parent.AddCommand(name, description, "", data)
	if err != nil {
		return err
	}
	r.actions[cmd] = action
	r.commandSources[cmd] = source
	for _, option := range groupOptions(cmd.Group) {
		r.sources[option] = source
	}
	return r.Validate()
}
func (r *Registry) Selected() *Action {
	cmd := r.Parser.Command
	for cmd.Active != nil {
		cmd = cmd.Active
	}
	action, ok := r.actions[cmd]
	if !ok {
		return nil
	}
	return &action
}
func (r *Registry) Values() cfg.Values {
	values := cfg.Values{}
	selected := map[*flags.Command]bool{}
	for cmd := r.Parser.Command; cmd != nil; cmd = cmd.Active {
		selected[cmd] = true
	}
	for _, b := range r.bindings {
		if b.key == "" || !selected[b.command] {
			continue
		}
		for _, o := range groupOptions(b.group) {
			if !o.IsSet() || o.IsSetDefault() {
				continue
			}
			name := o.Field().Tag.Get("config")
			if name == "" || name == "-" {
				continue
			}
			if values[b.key] == nil {
				values[b.key] = map[string]any{}
			}
			values[b.key][name] = o.Value()
		}
	}
	return values
}

// Validate rejects duplicate flags in each command before parsing or running.
// Child commands may shadow inherited flags, as with command-local --json.
func (r *Registry) Validate() error {
	var visit func(*flags.Command) error
	visit = func(cmd *flags.Command) error {
		names := map[string]string{}
		for _, option := range groupOptions(cmd.Group) {
			var keys []string
			if option.LongName != "" {
				keys = append(keys, "--"+option.LongNameWithNamespace())
			}
			if option.ShortName != 0 {
				keys = append(keys, "-"+string(option.ShortName))
			}
			for _, key := range keys {
				if previous, exists := names[key]; exists {
					return fmt.Errorf("duplicate flag %s on command %s (sources %s and %s)", key, cmd.Name, previous, r.source(option))
				}
				names[key] = r.source(option)
			}
		}
		for _, child := range cmd.Commands() {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(r.Parser.Command)
}
func groupOptions(group *flags.Group) []*flags.Option {
	out := append([]*flags.Option(nil), group.Options()...)
	for _, child := range group.Groups() {
		out = append(out, groupOptions(child)...)
	}
	return out
}

// ValueArity lets a host locate commands without mistaking a flag value for one.
func (r *Registry) ValueArity() map[string]int {
	values := map[string]int{}
	var visit func(*flags.Command)
	visit = func(cmd *flags.Command) {
		for _, option := range groupOptions(cmd.Group) {
			kind := option.Field().Type
			for kind.Kind() == reflect.Pointer {
				kind = kind.Elem()
			}
			arity := 1
			if kind.Kind() == reflect.Bool || option.OptionalArgument {
				arity = 0
			}
			if option.LongName != "" {
				values["--"+option.LongNameWithNamespace()] = arity
			}
			if option.ShortName != 0 {
				values["-"+string(option.ShortName)] = arity
			}
		}
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(r.Parser.Command)
	return values
}

func (r *Registry) source(option *flags.Option) string {
	if source := r.sources[option]; source != "" {
		return source
	}
	return "host"
}
func (r *Registry) writable(source string) error {
	if r.sealed {
		return fmt.Errorf("CLI declarations are sealed")
	}
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("CLI source is required")
	}
	return nil
}
func (r *Registry) Seal() error {
	if err := r.Validate(); err != nil {
		return err
	}
	r.sealed = true
	return nil
}
func (r *Registry) Parse(args []string) ([]string, error) {
	if err := r.Seal(); err != nil {
		return nil, err
	}
	return r.Parser.ParseArgs(args)
}

func (r *Registry) validateOptions(command string, existing, incoming []*flags.Option, source string) error {
	names := map[string]string{}
	check := func(options []*flags.Option, incoming bool) error {
		for _, option := range options {
			owner := r.source(option)
			if incoming {
				owner = source
			}
			var keys []string
			if option.LongName != "" {
				keys = append(keys, "--"+option.LongNameWithNamespace())
			}
			if option.ShortName != 0 {
				keys = append(keys, "-"+string(option.ShortName))
			}
			for _, key := range keys {
				if previous, exists := names[key]; exists {
					return fmt.Errorf("duplicate flag %s on command %s (sources %s and %s)", key, command, previous, owner)
				}
				names[key] = owner
			}
		}
		return nil
	}
	if err := check(existing, false); err != nil {
		return err
	}
	return check(incoming, true)
}
