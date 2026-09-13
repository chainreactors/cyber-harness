// Package workspace assembles explicitly selected file tools, event output,
// and read-only instruction mounts for the runner entrypoint.
package workspace

import (
	"context"
	"fmt"
	"slices"
	"sync"

	coreevents "github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/tool"
	eventoutput "github.com/chainreactors/aiscan/pkg/exts/eventoutput"
	fileext "github.com/chainreactors/aiscan/pkg/exts/files"
	observeext "github.com/chainreactors/aiscan/pkg/exts/observe"
	skillmount "github.com/chainreactors/aiscan/pkg/exts/skills"
	"github.com/chainreactors/aiscan/pkg/toolset"
	files "github.com/chainreactors/aiscan/tools/files"
)

type Config struct {
	// Nil selects files. A non-nil selection is exact; dependencies are never
	// installed implicitly. Restart with a new profile to change selection.
	Extensions      []string
	Files           files.Config
	Output          string
	SkillsDirectory string
}

type Profile struct {
	mu              sync.RWMutex
	set             *extension.Set
	registry        *toolset.Registry
	events          *coreevents.Stream
	selected        []string
	skills          *skillmount.Extension
	active, closing bool
}

func Available() []string { return []string{"files", "observe", "skills"} }

func New(config Config) (*Profile, error) {
	selected := slices.Clone(config.Extensions)
	if config.Extensions == nil {
		selected = []string{"files"}
	}
	seen := make(map[string]bool, len(selected))
	for _, id := range selected {
		if !slices.Contains(Available(), id) {
			return nil, fmt.Errorf("unknown workspace extension: %s", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate workspace extension: %s", id)
		}
		seen[id] = true
	}
	if !seen["files"] {
		return nil, fmt.Errorf("workspace selection requires files")
	}
	if !seen["skills"] && config.SkillsDirectory != "" {
		return nil, fmt.Errorf("skills directory configured without skills extension")
	}
	hookRegistry := hooks.New()
	events := coreevents.New()
	p := &Profile{selected: selected, registry: toolset.NewRegistry(hookRegistry), events: events}
	entries := []extension.Entry{}
	dependencies := []string{}
	fileConfig := config.Files
	if config.Output != "" {
		output, outputErr := eventoutput.New(events, eventoutput.Options{Path: config.Output})
		if outputErr != nil {
			return nil, outputErr
		}
		entries = append(entries, extension.Entry{ID: "event-output", Extension: output})
		dependencies = append(dependencies, "event-output")
	}
	if seen["observe"] {
		observer, observeErr := observeext.New(hookRegistry, events, observeext.Options{Kinds: []observeext.Kind{observeext.Tools, observeext.Files}})
		if observeErr != nil {
			return nil, observeErr
		}
		entries = append(entries, extension.Entry{ID: "observe", DependsOn: append([]string(nil), dependencies...), Extension: observer})
		dependencies = append(dependencies, "observe")
	}
	f, err := fileext.New(p.registry, hookRegistry, fileConfig)
	if err != nil {
		return nil, err
	}
	entries = append(entries, extension.Entry{ID: "files", DependsOn: dependencies, Extension: f})
	if seen["skills"] {
		p.skills, err = skillmount.New(f.Files(), config.SkillsDirectory)
		if err != nil {
			return nil, err
		}
		entries = append(entries, extension.Entry{ID: "skills", DependsOn: []string{"files"}, Extension: p.skills})
	}
	entries = append(entries, extension.Entry{ID: "tool-registry", DependsOn: []string{"files"}, Extension: p.registry})
	p.set, err = extension.New(entries...)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Events is the canonical stream produced by selected observers.
func (p *Profile) Events() *coreevents.Stream {
	if p == nil || p.events == nil {
		return nil
	}
	return p.events
}

func (p *Profile) Load(ctx context.Context) error {
	p.mu.RLock()
	closing := p.closing
	p.mu.RUnlock()
	if closing {
		return toolset.ErrUnavailable
	}
	if err := p.set.Load(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closing {
		return toolset.ErrUnavailable
	}
	p.active = true
	return nil
}

func (p *Profile) Executor() (tool.Executor, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.active || p.closing {
		return nil, toolset.ErrUnavailable
	}
	return p.registry, nil
}

// Installed reports the complete selection only while the entire composition
// is active. Available describes compiled options without opening resources.
func (p *Profile) Installed() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.active || p.closing {
		return nil
	}
	return slices.Clone(p.selected)
}

func (p *Profile) SkillLocations() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.active || p.closing || p.skills == nil {
		return nil
	}
	return p.skills.Locations()
}

func (p *Profile) Close(ctx context.Context) error {
	p.mu.Lock()
	p.closing, p.active = true, false
	p.mu.Unlock()
	return p.set.Close(ctx)
}
