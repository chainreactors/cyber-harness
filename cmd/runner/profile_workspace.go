package main

import (
	"context"
	"fmt"
	"slices"

	coreevents "github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
	fileext "github.com/chainreactors/aiscan/pkg/exts/files"
	harnessext "github.com/chainreactors/aiscan/pkg/exts/harness"
	observeext "github.com/chainreactors/aiscan/pkg/exts/observe"
	signalsext "github.com/chainreactors/aiscan/pkg/exts/signals"
	skillmount "github.com/chainreactors/aiscan/pkg/exts/skills"
	telemetryext "github.com/chainreactors/aiscan/pkg/exts/telemetry"
	"github.com/chainreactors/aiscan/pkg/toolset"
	files "github.com/chainreactors/aiscan/tools/files"
)

type workspaceProfileConfig struct {
	// Nil selects files. A non-nil selection is exact; dependencies are never
	// installed implicitly. Restart with a new profile to change selection.
	Extensions      []string
	Files           files.Config
	Output          string
	SkillsDirectory string
}

type workspaceProfile struct {
	extensions *extension.Set
	registry   toolset.Runtime
	events     *coreevents.Stream
	selected   []string
	skills     *skillmount.Catalog
}

func availableWorkspaceExtensions() []string { return []string{"files", "observe", "skills"} }

func newWorkspaceProfile(config workspaceProfileConfig) (*workspaceProfile, error) {
	selected := slices.Clone(config.Extensions)
	if config.Extensions == nil {
		selected = []string{"files"}
	}
	seen := make(map[string]bool, len(selected))
	for _, id := range selected {
		if !slices.Contains(availableWorkspaceExtensions(), id) {
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
	signals := signalsext.New()
	hookRegistry := signals.Hooks()
	events := signals.Events()
	harness, err := harnessext.New(hookRegistry)
	if err != nil {
		return nil, err
	}
	p := &workspaceProfile{selected: selected, registry: harness.ToolRegistry(), events: events}
	entries := []extension.Entry{{ID: "signals", Extension: signals}}
	dependencies := []string{}
	fileConfig := config.Files
	if config.Output != "" {
		output, outputErr := telemetryext.New(events, telemetryext.Options{Path: config.Output})
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
	toolDependencies := []string{"files"}
	if seen["skills"] {
		skills, err := skillmount.New(f.Files(), config.SkillsDirectory)
		if err != nil {
			return nil, err
		}
		p.skills = skills.Catalog()
		entries = append(entries, extension.Entry{ID: "skills", DependsOn: []string{"files"}, Extension: skills})
		toolDependencies = append(toolDependencies, "skills")
	}
	entries = append(entries, extension.Entry{ID: "harness", DependsOn: toolDependencies, Extension: harness})
	p.extensions, err = extension.New(entries...)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Events is the canonical stream produced by selected observers.
func (p *workspaceProfile) Events() *coreevents.Stream {
	if p == nil || p.events == nil {
		return nil
	}
	return p.events
}

func (p *workspaceProfile) Load(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return fmt.Errorf("workspace profile is required")
	}
	return p.extensions.Load(ctx)
}

func (p *workspaceProfile) Executor() (tool.Executor, error) {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return nil, toolset.ErrUnavailable
	}
	return p.registry, nil
}

// Installed reports the complete selection only while the entire composition
// is active. Available describes compiled options without opening resources.
func (p *workspaceProfile) Installed() []string {
	if p == nil || p.extensions == nil || !p.extensions.Active() {
		return nil
	}
	return slices.Clone(p.selected)
}

func (p *workspaceProfile) SkillLocations() []string {
	if p == nil || p.extensions == nil || !p.extensions.Active() || p.skills == nil {
		return nil
	}
	return p.skills.Locations()
}

func (p *workspaceProfile) Close(ctx context.Context) error {
	if p == nil || p.extensions == nil {
		return nil
	}
	return p.extensions.Close(ctx)
}
