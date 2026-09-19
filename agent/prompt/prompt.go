// Package prompt composes model prompts from extension contributions.
package prompt

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	coreregistry "github.com/chainreactors/cyber/core/registry"
	"github.com/chainreactors/cyber/core/resource"
)

// Target identifies one model-facing prompt. System and request prompts use
// separate targets because they have different composition contracts.
type Target string

const (
	MainSystem       Target = "main.system"
	ScannerSystem    Target = "scanner.system"
	EvaluatorSystem  Target = "evaluator.system"
	EvaluatorRequest Target = "evaluator.request"
	CompactSystem    Target = "compact.system"
	CompactRequest   Target = "compact.request"
	CompactPrefix    Target = "compact.prefix"
)

// Stable section IDs let extensions replace or position content without
// depending on the built-in renderer implementation.
const (
	SectionIdentity      = "identity"
	SectionAuthorization = "authorization"
	SectionEnvironment   = "environment"
	SectionTools         = "tools"
	SectionCommands      = "commands"
	SectionSkills        = "skills"
	SectionLoadedSkills  = "loaded_skills"
	SectionPrinciples    = "principles"
	SectionConstraints   = "constraints"
	SectionInstructions  = "instructions"
	SectionEvaluator     = "evaluator"
	SectionCompact       = "compact"
	SectionRequest       = "request"
)

// Context is the typed render input shared by the built-in prompt targets.
// Extensions should read only the fields relevant to their declared targets.
type Context struct {
	Target     Target
	Agent      AgentContext
	Evaluation EvaluationContext
	Compaction CompactionContext
	// Payload carries target-specific input owned by the contributing extension.
	Payload any
}

type AgentContext struct {
	Name         string
	Model        string
	NodeName     string
	ScannerName  string
	OS           string
	Arch         string
	Hostname     string
	Now          time.Time
	Windows      bool
	Tools        []Tool
	ScannerDocs  string
	Skills       []Skill
	LoadedSkills []LoadedSkill
	Instructions string
}

type Tool struct {
	Name        string
	Description string
}

type Skill struct {
	Name        string
	Description string
	Location    string
}

type LoadedSkill struct {
	Name string
	Body string
}

type EvaluationContext struct {
	Goal     string
	Criteria string
	Progress string
	Trace    string
}

type CompactionContext struct {
	CustomInstructions string
}

type Renderer func(context.Context, Context) (string, error)

func Static(text string) Renderer {
	return func(context.Context, Context) (string, error) { return text, nil }
}

// Contribution mutates a named prompt document. Contributions are applied in
// extension registration order; the extension graph therefore remains the
// only ordering mechanism.
type Contribution struct {
	Name    string
	Targets []Target
	Apply   func(context.Context, *Document, Context) error
}

type Diagnostic struct {
	Contribution string
	Section      string
	Message      string
}

type Result struct {
	Prompt      string
	Diagnostics []Diagnostic
}

type Resolver interface {
	Build(context.Context, Context) Result
}

type section struct {
	id     string
	source string
	ctx    context.Context
	render Renderer
}

// Document is an ordered collection of independently renderable sections.
type Document struct {
	sections []section
	source   string
	ctx      context.Context
}

func (d *Document) clone() *Document {
	if d == nil {
		return &Document{}
	}
	return &Document{sections: append([]section(nil), d.sections...), source: d.source, ctx: d.ctx}
}

func (d *Document) Add(id string, render Renderer) error {
	if err := validateSection(id, render); err != nil {
		return err
	}
	if d.index(id) >= 0 {
		return fmt.Errorf("prompt section %q already exists", id)
	}
	d.sections = append(d.sections, section{id: id, source: d.source, ctx: d.ctx, render: render})
	return nil
}

func (d *Document) Replace(id string, render Renderer) error {
	if err := validateSection(id, render); err != nil {
		return err
	}
	index := d.index(id)
	if index < 0 {
		return fmt.Errorf("prompt section %q does not exist", id)
	}
	d.sections[index] = section{id: id, source: d.source, ctx: d.ctx, render: render}
	return nil
}

func (d *Document) Remove(id string) {
	index := d.index(id)
	if index < 0 {
		return
	}
	d.sections = append(d.sections[:index], d.sections[index+1:]...)
}

func (d *Document) Before(anchor, id string, render Renderer) error {
	return d.insert(anchor, id, render, false)
}

func (d *Document) After(anchor, id string, render Renderer) error {
	return d.insert(anchor, id, render, true)
}

func (d *Document) Reset(id string, render Renderer) error {
	if err := validateSection(id, render); err != nil {
		return err
	}
	d.sections = []section{{id: id, source: d.source, ctx: d.ctx, render: render}}
	return nil
}

func (d *Document) insert(anchor, id string, render Renderer, after bool) error {
	if err := validateSection(id, render); err != nil {
		return err
	}
	if d.index(id) >= 0 {
		return fmt.Errorf("prompt section %q already exists", id)
	}
	index := d.index(anchor)
	if index < 0 {
		return fmt.Errorf("prompt anchor %q does not exist", anchor)
	}
	if after {
		index++
	}
	d.sections = append(d.sections, section{})
	copy(d.sections[index+1:], d.sections[index:])
	d.sections[index] = section{id: id, source: d.source, ctx: d.ctx, render: render}
	return nil
}

func (d *Document) index(id string) int {
	for i := range d.sections {
		if d.sections[i].id == id {
			return i
		}
	}
	return -1
}

func validateSection(id string, render Renderer) error {
	if strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) || render == nil {
		return fmt.Errorf("prompt section requires a canonical id and renderer")
	}
	return nil
}

// Registry is both the Contribution Point and its read-only Resolver.
type Registry struct {
	store *coreregistry.Store[Contribution]
}

func NewRegistry() *Registry {
	return &Registry{store: coreregistry.New[Contribution]()}
}

func (r *Registry) Add(values ...Contribution) (resource.Handle, error) {
	if r == nil || r.store == nil || len(values) == 0 {
		return nil, coreregistry.ErrInvalid
	}
	entries := make([]coreregistry.Value[Contribution], 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.Name) == "" || value.Name != strings.TrimSpace(value.Name) || len(value.Targets) == 0 || value.Apply == nil {
			return nil, coreregistry.ErrInvalid
		}
		for _, target := range value.Targets {
			if name := string(target); strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) {
				return nil, coreregistry.ErrInvalid
			}
		}
		entries = append(entries, coreregistry.Value[Contribution]{Name: value.Name, Value: value})
	}
	return r.store.Add(entries...)
}

func (r *Registry) Activate(ctx context.Context) error {
	if r == nil || r.store == nil {
		return coreregistry.ErrUnavailable
	}
	return r.store.Activate(ctx)
}

func (r *Registry) Close(ctx context.Context) error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.Close(ctx)
}

func (r *Registry) Build(ctx context.Context, input Context) Result {
	if ctx == nil {
		ctx = context.Background()
	}
	document := &Document{}
	var diagnostics []Diagnostic
	if r == nil || r.store == nil {
		return Result{Diagnostics: []Diagnostic{{Message: coreregistry.ErrUnavailable.Error()}}}
	}
	var releases []func()
	defer func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}()
	for _, name := range r.store.Names() {
		entry, callCtx, release, err := r.store.Acquire(ctx, name)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Contribution: name, Message: err.Error()})
			continue
		}
		contribution := entry.Value
		if !targets(contribution.Targets, input.Target) {
			release()
			continue
		}
		candidate := document.clone()
		candidate.source = contribution.Name
		candidate.ctx = callCtx
		if err := apply(callCtx, contribution, candidate, input); err != nil {
			diagnostics = append(diagnostics, Diagnostic{Contribution: contribution.Name, Message: err.Error()})
			release()
			continue
		}
		releases = append(releases, release)
		document = candidate
	}
	parts := make([]string, 0, len(document.sections))
	for _, section := range document.sections {
		if err := ctx.Err(); err != nil {
			diagnostics = append(diagnostics, Diagnostic{Contribution: section.source, Section: section.id, Message: err.Error()})
			break
		}
		renderCtx := section.ctx
		if renderCtx == nil {
			renderCtx = ctx
		}
		content, err := render(renderCtx, section, input)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Contribution: section.source, Section: section.id, Message: err.Error()})
			continue
		}
		if content = strings.TrimSpace(content); content != "" {
			parts = append(parts, content)
		}
	}
	return Result{Prompt: strings.Join(parts, "\n\n"), Diagnostics: diagnostics}
}

func apply(ctx context.Context, contribution Contribution, document *Document, input Context) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("prompt contribution panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	return contribution.Apply(ctx, document, input)
}

func render(ctx context.Context, value section, input Context) (text string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("prompt renderer panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	return value.render(ctx, input)
}

func targets(values []Target, target Target) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var (
	_ Resolver                     = (*Registry)(nil)
	_ resource.Point[Contribution] = (*Registry)(nil)
)
