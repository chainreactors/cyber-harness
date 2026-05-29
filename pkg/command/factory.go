package command

import "sync"

type Factory struct {
	Group string
	Build func(deps *Deps, reg *CommandRegistry)
}

type Deps struct {
	WorkDir     string
	BashTimeout int
	SkillStore  any

	EngineSet    any
	Resources    any
	IOAClient    any
	Provider     any
	Model        string
	ScannerProxy string
	ScanOpts     []any
	Logger       any
	NodeName     string
	NodeMeta     map[string]any
	TavilyKeys string // comma-separated Tavily API keys (build-time fallback)
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

func BuildAll(deps *Deps, reg *CommandRegistry) {
	factoryMu.Lock()
	snapshot := make([]Factory, len(factories))
	copy(snapshot, factories)
	factoryMu.Unlock()

	for _, f := range snapshot {
		f.Build(deps, reg)
	}
}

func BuildGroup(group string, deps *Deps, reg *CommandRegistry) {
	factoryMu.Lock()
	snapshot := make([]Factory, len(factories))
	copy(snapshot, factories)
	factoryMu.Unlock()

	for _, f := range snapshot {
		if f.Group != group {
			continue
		}
		f.Build(deps, reg)
	}
}
