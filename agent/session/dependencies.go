package session

import (
	"context"
	"errors"
	"fmt"

	eventjsonl "github.com/chainreactors/cyber/core/events/jsonl"
	"github.com/chainreactors/cyber/core/telemetry"
)

// HistoryStore supplies recovery without coupling the runtime to a filesystem.
// Event persistence uses the same canonical event stream as live publication.
type HistoryStore interface {
	Load(context.Context, string) (*History, error)
	Validate(string) error
}
type JSONLHistory struct{}

func (JSONLHistory) Load(ctx context.Context, path string) (*History, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ReadHistory(path)
}
func (JSONLHistory) Validate(path string) error { return eventjsonl.ValidateJSONLTarget(path) }

// Resource owns one Runtime installation. Consumers borrow Runtime while the
// owning extension alone starts and closes Resource.
type Resource struct {
	runtime *Runtime
}

func (r *Resource) Runtime() *Runtime {
	if r == nil {
		return nil
	}
	return r.runtime
}

// NewResource is inert. Start and Close are explicit owner operations and do
// not require an extension.Scope.
func NewResource(config Config) (*Resource, error) {
	declared, index, err := commandDeclarations(config.Commands)
	if err != nil {
		return nil, err
	}
	for _, dependency := range []struct {
		name    string
		missing bool
	}{
		{"loop", config.Loop == nil}, {"providers", config.Providers == nil},
		{"events", config.Events == nil}, {"hooks", config.Hooks == nil},
		{"tools", config.Tools == nil}, {"commands", config.CommandRegistry == nil},
		{"skills", config.Skills == nil},
	} {
		if dependency.missing {
			return nil, fmt.Errorf("session requires %s", dependency.name)
		}
	}

	if config.Logger == nil {
		config.Logger = telemetry.NewLoggerRef(nil)
	}
	config.BaseSkills = append([]string(nil), config.BaseSkills...)
	config.SelectedSkills = append([]string(nil), config.SelectedSkills...)
	config.Commands = declared
	return &Resource{runtime: &Runtime{
		commands: declared, commandIndex: index, history: config.History,
		providers: config.Providers, events: config.Events, logger: config.Logger, config: config,
		hooks: config.Hooks, tools: config.Tools, commandRegistry: config.CommandRegistry,
		skills: config.Skills, shell: config.Shell,
		sessions: make(map[string]*sessionState), runs: make(map[string]*Run),
		closeDone: make(chan struct{}),
	}}, nil
}

var ErrUnavailable = errors.New("session runtime is unavailable")
