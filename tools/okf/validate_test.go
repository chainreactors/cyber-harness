package okf

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/pkg/commands"
)

func writeMarkdown(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hasRule(report Report, rule string, level Level) bool {
	for _, issue := range report.Issues {
		if issue.Rule == rule && issue.Level == level {
			return true
		}
	}
	return false
}

func TestValidateAcceptsOfficialMinimalConcept(t *testing.T) {
	root := t.TempDir()
	writeMarkdown(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n\n# Concepts\n\n- [Minimal](minimal.md)\n")
	writeMarkdown(t, root, "minimal.md", "---\ntype: Reference\ncustom_extension: preserved\n---\n\n# Minimal\n")
	report, err := Validate(t.Context(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid() {
		t.Fatalf("minimal OKF bundle is invalid: %#v", report.Issues)
	}
	if !hasRule(report, "concept.title", Warning) || !hasRule(report, "concept.description", Warning) {
		t.Fatalf("recommended metadata warnings missing: %#v", report.Issues)
	}
}

func TestValidateRejectsConformanceFailures(t *testing.T) {
	root := t.TempDir()
	writeMarkdown(t, root, "missing-type.md", "---\ntitle: Missing type\n---\n\n# Missing\n")
	report, err := Validate(t.Context(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid() || !hasRule(report, "concept.type", Error) {
		t.Fatalf("report = %#v", report)
	}
}

func TestValidateAcceptsFrontmatterEndingAtEOF(t *testing.T) {
	root := t.TempDir()
	writeMarkdown(t, root, "empty-body.md", "---\ntype: Reference\ntitle: Empty body\ndescription: A concept body may be empty.\n---")
	report, err := Validate(t.Context(), root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid() {
		t.Fatalf("report = %#v", report)
	}
}

func TestStrictModePromotesProductionGuidance(t *testing.T) {
	root := t.TempDir()
	writeMarkdown(t, root, "finding.md", "---\ntype: Finding\nstatus: confirmed\n---\n\n# Finding\n")
	conformance, err := Validate(t.Context(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !conformance.Valid() || !hasRule(conformance, "lifecycle.status", Warning) {
		t.Fatalf("validate report = %#v", conformance)
	}
	strict, err := Validate(t.Context(), root, true)
	if err != nil {
		t.Fatal(err)
	}
	if strict.Valid() || !hasRule(strict, "lifecycle.status", Error) {
		t.Fatalf("test report = %#v", strict)
	}
}

func TestUnknownVersionIsBestEffortUnlessStrict(t *testing.T) {
	root := t.TempDir()
	writeMarkdown(t, root, "index.md", "---\nokf_version: \"9.0\"\n---\n\n# Concepts\n")
	conformance, err := Validate(t.Context(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !conformance.Valid() || !hasRule(conformance, "version", Warning) {
		t.Fatalf("validate report = %#v", conformance)
	}
	strict, err := Validate(t.Context(), root, true)
	if err != nil {
		t.Fatal(err)
	}
	if strict.Valid() || !hasRule(strict, "version", Error) {
		t.Fatalf("test report = %#v", strict)
	}
}

func TestStrictAttestedComputationRequiresRunnableReferences(t *testing.T) {
	root := t.TempDir()
	writeMarkdown(t, root, "computation.md", `---
type: Attested Computation
title: Revenue
description: Computes revenue.
runtime: bigquery
status: stable
generated: { by: process:test, at: 2026-09-18T10:00:00Z }
verified: { by: human:reviewer, at: 2026-09-18T11:00:00+08:00 }
stale_after: 2027-09-18T10:00:00Z
sources:
  - resource: https://example.com/policy
    last_modified: 2026-09-17T10:00:00Z
usage_window: { from: 2026-09-01T00:00:00Z, to: 2026-09-18T00:00:00Z }
executor: { resource: run.md, receipt: [job_id, result] }
attester: { resource: attest.py }
---

# Computation

`+"```sql"+`
SELECT 1
`+"```"+`
`)
	report, err := Validate(t.Context(), root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid() {
		t.Fatalf("strict computation report = %#v", report.Issues)
	}
}

func TestCommandSupportsJSONAndStrictFailure(t *testing.T) {
	root := t.TempDir()
	writeMarkdown(t, root, "minimal.md", "---\ntype: Reference\n---\n\n# Minimal\n")
	command := NewCommand()
	var output bytes.Buffer
	result, err := command.Run(context.Background(), &commands.Execution{
		Args: []string{"validate", ".", "--format", "json"}, Dir: root, Stdout: &output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.(Report); !ok {
		t.Fatalf("result type = %T", result)
	}
	var report Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON output %q: %v", output.String(), err)
	}
	if !report.Valid() || report.Strict {
		t.Fatalf("validate report = %#v", report)
	}

	output.Reset()
	_, err = command.Run(context.Background(), &commands.Execution{
		Args: []string{"test", "."}, Dir: root, Stdout: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "OKF test failed") || !strings.Contains(output.String(), "ERROR") {
		t.Fatalf("strict command error=%v output=%q", err, output.String())
	}
}
