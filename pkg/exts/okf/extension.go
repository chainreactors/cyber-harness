// Package okf contributes OKF prompt policy, validation commands, and docs.
package okf

import (
	"context"
	"fmt"

	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/core/extension"
	okftool "github.com/chainreactors/cyber/tools/okf"
)

// Extension contributes the OKF prompt policy, validation command, and
// reference docs. The prompt targets are configurable so distributions
// decide which agents receive the policy.
type Extension struct {
	targets []prompt.Target
}

// New builds the extension. Without targets the policy applies to the main
// system prompt only.
func New(targets ...prompt.Target) *Extension {
	if len(targets) == 0 {
		targets = []prompt.Target{prompt.MainSystem}
	}
	return &Extension{targets: targets}
}

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
		Name: "okf.markdown", Targets: e.targets,
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
