// Package workspace assembles explicitly selected file tools, audit journaling,
// and read-only instruction mounts for the runner entrypoint.
package workspace

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/extensions/toolgroup"
	"github.com/chainreactors/aiscan/pkg/fileaudit"
	"github.com/chainreactors/aiscan/pkg/files"
	"github.com/chainreactors/aiscan/pkg/recording"
	"github.com/chainreactors/aiscan/pkg/skillmount"
	"github.com/chainreactors/aiscan/pkg/toolset/filetools"
	"github.com/chainreactors/aiscan/pkg/toolset/registry"
)

type Config struct {
	// Nil selects files. A non-nil selection is exact; dependencies are never
	// installed implicitly. Restart with a new profile to change selection.
	Extensions      []string
	Files           files.Config
	AuditLog        string
	SkillsDirectory string
}

type Profile struct {
	mu              sync.RWMutex
	set             *extension.Set
	registry        *registry.Registry
	selected        []string
	skills          *skillmount.Extension
	active, closing bool
}

func Available() []string { return []string{"files", "file-audit", "skills"} }

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
	if seen["file-audit"] && config.AuditLog == "" {
		return nil, fmt.Errorf("file-audit requires a journal path")
	}
	if !seen["file-audit"] && config.AuditLog != "" {
		return nil, fmt.Errorf("audit journal configured without file-audit extension")
	}
	if !seen["skills"] && config.SkillsDirectory != "" {
		return nil, fmt.Errorf("skills directory configured without skills extension")
	}
	f, err := files.New(config.Files)
	if err != nil {
		return nil, err
	}
	r := registry.New()
	p := &Profile{registry: r, selected: selected}
	entries := []extension.Entry{{ID: "registry", Extension: r}, {ID: "filesystem", Extension: f}}
	dependencies := []string{"registry", "filesystem"}
	if seen["file-audit"] {
		audit, err := fileaudit.NewWithFiles(f)
		if err != nil {
			return nil, err
		}
		journal := recording.NewFileLog(audit, config.AuditLog)
		entries = append(entries,
			extension.Entry{ID: "file-journal", Extension: journal},
			extension.Entry{ID: "file-audit", DependsOn: []string{"filesystem", "file-journal"}, Extension: audit})
		dependencies = append(dependencies, "file-audit")
	}
	if seen["skills"] {
		p.skills, err = skillmount.New(f, config.SkillsDirectory)
		if err != nil {
			return nil, err
		}
		entries = append(entries, extension.Entry{ID: "skills", DependsOn: []string{"filesystem"}, Extension: p.skills})
		dependencies = append(dependencies, "skills")
	}
	definitions, err := filetools.Tools(f)
	if err != nil {
		return nil, err
	}
	fileTools, err := toolgroup.New(r, definitions...)
	if err != nil {
		return nil, err
	}
	entries = append(entries, extension.Entry{ID: "files", DependsOn: dependencies, Extension: fileTools})
	p.set, err = extension.New(entries...)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Profile) Load(ctx context.Context) error {
	p.mu.RLock()
	closing := p.closing
	p.mu.RUnlock()
	if closing {
		return registry.ErrUnavailable
	}
	if err := p.set.Load(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closing {
		return registry.ErrUnavailable
	}
	p.active = true
	return nil
}

func (p *Profile) Executor() (tool.Executor, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.active || p.closing {
		return nil, registry.ErrUnavailable
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
