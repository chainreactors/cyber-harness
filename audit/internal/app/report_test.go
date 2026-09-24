package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestTaskResolutionHasNoScannerFallback(t *testing.T) {
	for _, test := range []struct {
		option  cfg.Option
		oneShot bool
	}{{cfg.Option{}, false}, {cfg.Option{AgentOptions: cfg.AgentOptions{Inputs: []string{"src"}}}, true}, {cfg.Option{AgentOptions: cfg.AgentOptions{Prompt: "Review authorization"}}, true}, {cfg.Option{AgentOptions: cfg.AgentOptions{Resume: "session.jsonl"}}, false}} {
		task, oneShot, err := resolveAuditTask(&test.option)
		if err != nil {
			t.Fatal(err)
		}
		if oneShot != test.oneShot || strings.Contains(task, "using scan") {
			t.Fatalf("task=%q oneShot=%v", task, oneShot)
		}
	}
	file := filepath.Join(t.TempDir(), "task.txt")
	os.WriteFile(file, []byte("inspect ownership"), 0600)
	task, oneShot, err := resolveAuditTask(&cfg.Option{AgentOptions: cfg.AgentOptions{TaskFile: file}})
	if err != nil || !oneShot || task != "inspect ownership" {
		t.Fatalf("%q %v %v", task, oneShot, err)
	}
}
func completeReport(t *testing.T, r *runReport) {
	t.Helper()
	c := coverage{Reviewed: true, Scope: "test fixture", Examined: []string{"src/handler.ts"}, Excluded: []string{}, Unsupported: []string{}, Unresolved: []string{}, Checks: []checkRecord{{Tool: "osv-scanner", Status: "not_applicable", Evidence: "No dependency manifests in this fixture"}, {Tool: "proton", Status: "completed", Evidence: "raw/proton.jsonl"}}}
	if err := writeJSON(filepath.Join(r.Directory, "coverage.json"), c); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Directory, "raw", "proton.jsonl"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Directory, "index.md"), []byte("---\nokf_version: \"0.2\"\n---\n\n# Audit results\n\nSee [coverage](coverage.json) and [findings](findings.json).\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestReportRequiresCoverageAndEvidence(t *testing.T) {
	r, err := newReport(t.Context(), t.TempDir(), "", "test", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.validate(t.Context()); err == nil {
		t.Fatal("empty report was accepted")
	}
	completeReport(t, r)
	if err := r.validate(t.Context()); err != nil {
		t.Fatal(err)
	}
	f := finding{ID: "AUD-001", Title: "Unproven advisory", Status: "confirmed", Verification: "static"}
	writeJSON(filepath.Join(r.Directory, "findings.json"), []finding{f})
	if err := r.validate(t.Context()); err == nil {
		t.Fatal("unproven confirmed finding accepted")
	}
	if err := r.finish(t.Context(), context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(r.Directory, "run.json"))
	var stored runReport
	json.Unmarshal(data, &stored)
	if stored.Status != "interrupted" || stored.Finished == nil {
		t.Fatalf("%+v", stored)
	}
	if _, err := newReport(t.Context(), r.Workspace, r.Directory, "test", "", nil); err == nil {
		t.Fatal("existing report overwritten")
	}
}
func TestReportExclusionsAndEvidencePaths(t *testing.T) {
	workspace := t.TempDir()
	r, err := newReport(t.Context(), workspace, "custom-report", "test", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	config, patterns, err := r.searchExclusions()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patterns, "!custom-report/**") {
		t.Fatal(patterns)
	}
	if _, err := os.Stat(config); err != nil {
		t.Fatal(err)
	}
	if err := reportEvidence(r.Directory, "../outside"); err == nil {
		t.Fatal("escaping evidence accepted")
	}
	failure := errors.New("provider failure")
	if err := r.finish(t.Context(), failure); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if r.Status != "failed" {
		t.Fatal(r.Status)
	}
}

func TestReportFinalization(t *testing.T) {
	for _, test := range []struct {
		name, status         string
		complete, invalidOKF bool
		runErr               error
	}{
		{name: "valid", status: "completed", complete: true},
		{name: "unfinished", status: "incomplete"},
		{name: "invalid OKF", status: "incomplete", complete: true, invalidOKF: true},
		{name: "provider error", status: "failed", runErr: errors.New("provider failed")},
		{name: "canceled", status: "interrupted", runErr: context.Canceled},
		{name: "timeout", status: "interrupted", runErr: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, err := newReport(t.Context(), t.TempDir(), "", "review", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if test.complete {
				completeReport(t, r)
			}
			if test.invalidOKF {
				if err := os.WriteFile(filepath.Join(r.Directory, "index.md"), []byte("---\nokf_version: '0.2'\nextra: invalid\n---\n# Report\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err = r.finish(t.Context(), test.runErr)
			if (err == nil) != (test.status == "completed") {
				t.Fatalf("unexpected completion: %v", err)
			}
			if test.runErr != nil && !errors.Is(err, test.runErr) {
				t.Fatalf("lost run error: %v", err)
			}
			body, err := os.ReadFile(filepath.Join(r.Directory, "run.json"))
			if err != nil {
				t.Fatal(err)
			}
			var saved runReport
			if err := json.Unmarshal(body, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Status != test.status || saved.Finished == nil {
				t.Fatalf("saved report: %+v", saved)
			}
		})
	}
}

func TestReportFinalizationReturnsWriteFailure(t *testing.T) {
	r, err := newReport(t.Context(), t.TempDir(), "", "review", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	completeReport(t, r)
	path := filepath.Join(r.Directory, "run.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := r.finish(t.Context(), nil); err == nil {
		t.Fatal("report write failure was ignored")
	}
}
