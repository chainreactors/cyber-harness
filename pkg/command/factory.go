package command

import "sync"

type Factory struct {
	Group string
	Build func(deps *Deps) []PseudoCommand
}

type Deps struct {
	EngineSet any
	Resources any
	ACPClient any
	Provider  any
	Model     string
	ScanOpts  []any
	Logger    any
	NodeName  string
	NodeMeta  map[string]any
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

type GroupedCommand struct {
	Cmd   PseudoCommand
	Group string
}

func BuildAll(deps *Deps) []GroupedCommand {
	factoryMu.Lock()
	snapshot := make([]Factory, len(factories))
	copy(snapshot, factories)
	factoryMu.Unlock()

	var result []GroupedCommand
	for _, f := range snapshot {
		cmds := f.Build(deps)
		for _, cmd := range cmds {
			result = append(result, GroupedCommand{Cmd: cmd, Group: f.Group})
		}
	}
	return result
}

func BuildGroup(group string, deps *Deps) []PseudoCommand {
	factoryMu.Lock()
	snapshot := make([]Factory, len(factories))
	copy(snapshot, factories)
	factoryMu.Unlock()

	var result []PseudoCommand
	for _, f := range snapshot {
		if f.Group != group {
			continue
		}
		result = append(result, f.Build(deps)...)
	}
	return result
}
