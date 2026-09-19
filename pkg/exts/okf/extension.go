// Package okf contributes OKF prompt policy, validation commands, and docs.
package okf

import (
	"context"
	"fmt"

	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/core/extension"
	okftool "github.com/chainreactors/cyber/tools/okf"
)

type Extension struct{}

func New() *Extension { return &Extension{} }

const (
	// SectionMarkdown is the prompt section contributed by this extension.
	SectionMarkdown = "okf.markdown"
	// ReferenceURI is the virtual documentation installed with the command.
	ReferenceURI = okftool.ReferenceURI
)

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || scope == nil {
		return fmt.Errorf("OKF extension is unavailable")
	}
	if err := extension.Add(scope, prompt.Contribution{
		Name: "okf.markdown", Targets: []prompt.Target{
			prompt.MainSystem, prompt.ScannerSystem,
		},
		Apply: applyPolicy,
	}); err != nil {
		return err
	}
	if err := extension.Add(scope, okftool.NewCommand()); err != nil {
		return err
	}
	return extension.Add(scope, referenceBundle())
}

func applyPolicy(_ context.Context, document *prompt.Document, _ prompt.Context) error {
	return document.Add(SectionMarkdown, prompt.Static(okfPolicy))
}

const okfPolicy = `## Open Knowledge Format

When creating or modifying Markdown files, organize them as an OKF 0.2 knowledge bundle. Concept files require YAML frontmatter with a non-empty type. Use index.md for progressive disclosure, log.md for dated history, bundle-relative Markdown links, and the standard sources, generated, verified, status, and stale_after fields when applicable. Preserve unknown extension fields.

Before completing Markdown deliverables, run ` + "`okf validate <path>`" + `. For maintained or published bundles also run ` + "`okf test <path>`" + ` and resolve strict production failures. These rules apply to Markdown artifacts, not ordinary chat responses.`

var _ extension.Extension = (*Extension)(nil)
