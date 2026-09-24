// Package audit owns the audit identity, workflow and report context.
package audit

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/tools/files"
)

//go:embed skills/*.md
var content embed.FS

//go:embed skills/workflow.md
var workflow string

const URI = "cyber://skills/audit/"

type Config struct{ Workspace, ReportDir, ToolSummary, SearchExclusions string }
type Extension struct {
	config Config
	files  *files.Files
}

func New(config Config) *Extension { return &Extension{config: config} }
func (e *Extension) Load(scope *extension.Scope) error {
	fileSystem, err := extension.Use[*files.Files](scope)
	if err != nil {
		return err
	}
	tree, err := fs.Sub(content, "skills")
	if err != nil {
		return err
	}
	if err := fileSystem.Mount(URI, tree); err != nil {
		return err
	}
	e.files = fileSystem
	return extension.Add(scope, prompt.Contribution{Name: "audit.identity", Targets: []prompt.Target{prompt.MainSystem}, Apply: func(_ context.Context, doc *prompt.Document, _ prompt.Context) error {
		if err := doc.Replace(prompt.SectionIdentity, prompt.Static(identity+"\n\n"+workflow)); err != nil {
			return err
		}
		if err := doc.After(prompt.SectionTools, prompt.SectionCommands, func(_ context.Context, input prompt.Context) (string, error) {
			return "## Commands available through bash\n\n" + input.Agent.CommandDocs + "\nUse the external CLIs listed in Tool versions directly by name. Read cyber://skills/audit/tools.md for recipes. Proton is a built-in command; documentation: cyber://proton/proton.md.", nil
		}); err != nil {
			return err
		}
		return doc.Add("audit.workspace", prompt.Static(fmt.Sprintf("## Audit workspace\n\nRepository: %s\nReport directory: %s\nTool versions:\n%s\n\nSearch exclusions:\n%s\n\nPreserve every occurrence and location as evidence. File-tool paths are repository-relative; use bash to write a --report-dir outside the repository. Store coverage.json, findings.json, raw tool output and the final OKF index.md in the report directory. session.jsonl is recorded by the harness. Do not edit run.json or session.jsonl. Exclude audit artifacts from subsequent repository searches and scans.", e.config.Workspace, e.config.ReportDir, e.config.ToolSummary, e.config.SearchExclusions)))
	}})
}
func (e *Extension) Close(ctx context.Context) error {
	if e.files == nil {
		return nil
	}
	err := e.files.Unmount(ctx, URI)
	if err == nil {
		e.files = nil
	}
	return err
}

const identity = `You are cyber-audit, a model-led source and binary auditor inside cyber-harness. Discover vulnerabilities using your own reasoning about data flow, trust boundaries, authorization, state and business logic. Use tools to gather and verify evidence. A text occurrence is not a symbol reference; an AST match is not a vulnerability; a dependency advisory does not establish exploitability. Work in the supplied repository and respect the requested scope. Investigate plausible paths, challenge your assumptions, inspect protections and seek counterexamples before confirming a finding. Record uncertainty and coverage honestly. Do not treat an unsupported language, an error, or an unexamined area as clean. Do not modify target code unless the task requests a fix; put experiments and reports in the report directory.`

var _ extension.Extension = (*Extension)(nil)
