package commands

import (
	"sync"

	"github.com/chainreactors/aiscan/agent/hooks"
	"github.com/chainreactors/aiscan/agent/provider"
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
)

type Factory struct {
	Capability capability.ID
	Build      func(deps *Deps, reg *CommandRegistry)
}

// SkillSource is the slice of skills.Store the built-in tools need; declaring
// it here keeps pkg/commands from importing the skill store itself.
type SkillSource interface {
	VirtualFileReader
	VirtualGlobber
}

// Deps carries everything a Factory may need. Values whose type pkg/commands
// must not import (scanner engines, resources, IOA client, scan options) travel
// in the Bag under keys owned by their own package.
type Deps struct {
	*deps.Bag

	WorkDir     string
	BashTimeout int
	SkillStore  SkillSource
	RunnerMode  bool

	Provider          provider.Provider
	ScannerProxy      string
	Logger            telemetry.Logger
	NodeName          string
	NodeMeta          map[string]any
	TavilyKeys        string // comma-separated Tavily API keys
	PlaywrightSession string
	DataBus           *eventbus.Bus[output.ToolDataEvent]
	Hooks             *hooks.Registry
}

// Provide stores a typed dependency, allocating the bag on first use so a
// literal-constructed Deps cannot drop it silently.
func Provide[T any](d *Deps, key deps.Key[T], value T) {
	if d == nil {
		return
	}
	if d.Bag == nil {
		d.Bag = deps.New()
	}
	deps.Set(d.Bag, key, value)
}

func (d *Deps) GetLogger() telemetry.Logger {
	if d != nil && d.Logger != nil {
		return d.Logger
	}
	return telemetry.NopLogger()
}

// Skip reports that a factory bailed out because a dependency is missing. It
// exists because these sites used to return silently, leaving the tool absent
// from the registry with nothing in the log to explain it.
func (d *Deps) Skip(id, dep string) {
	d.GetLogger().Warnf("%s", telemetry.StartupLine("skip", id, "missing dependency "+dep))
}

var (
	factoryMu sync.Mutex
	factories []Factory
)

func RegisterFactory(f Factory) {
	factoryMu.Lock()
	defer factoryMu.Unlock()
	factories = append(factories, f)
}

// BuildPlan is the only factory assembly path. Factory membership is keyed by
// capability identity; groups are selection metadata owned by descriptors.
func BuildPlan(plan capability.Plan, deps *Deps, reg *CommandRegistry) {
	factoryMu.Lock()
	snapshot := make([]Factory, len(factories))
	copy(snapshot, factories)
	factoryMu.Unlock()

	for _, f := range snapshot {
		if f.Capability == "" || !plan.Has(f.Capability) {
			continue
		}
		f.Build(deps, reg)
	}
}
