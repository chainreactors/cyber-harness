package app

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveModelAudit(t *testing.T) {
	if os.Getenv("CYBER_AUDIT_LIVE") != "1" {
		t.Skip("set CYBER_AUDIT_LIVE=1 with a configured provider")
	}
	data := os.Getenv("AUDIT_TEST_DATA_DIR")
	if data == "" {
		t.Fatal("AUDIT_TEST_DATA_DIR is required")
	}
	data, err := filepath.Abs(data)
	if err != nil {
		t.Fatal(err)
	}
	evaluationDir := filepath.Join(data, "evaluations")
	if err := os.MkdirAll(evaluationDir, 0700); err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(evaluationDir, "model-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Live evaluation retained at %s", workspace)
	for _, name := range []string{"handlers.py", "README.md"} {
		body, err := os.ReadFile(filepath.Join("..", "..", "tests", "testdata", "repository", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	report := filepath.Join(workspace, "report")
	if err := Run(t.Context(), []string{"--workdir", workspace, "--data-dir", data, "--report-dir", report, "-p", "Audit this service for exploitable vulnerabilities. Inspect protections and business logic, and provide evidence and coverage.", "--timeout", "480", "--quiet", "--no-color"}, io.Discard, os.Stderr); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(report, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var findings []finding
	if err := json.Unmarshal(body, &findings); err != nil {
		t.Fatal(err)
	}
	shell, tenant := false, false
	for _, f := range findings {
		if f.Status != "confirmed" {
			continue
		}
		if strings.Contains(f.Location, "handlers.py:6") {
			shell = true
		}
		if strings.Contains(f.Location, "handlers.py:19") || strings.Contains(f.Location, "handlers.py:20") {
			tenant = true
		}
		if strings.Contains(f.Location, "handlers.py:13") || strings.Contains(f.Location, "handlers.py:29") {
			t.Errorf("protected path falsely confirmed: %s", f.Title)
		}
	}
	if !shell || !tenant {
		t.Fatalf("required paths missing: shell=%v tenant=%v; inspect model trace", shell, tenant)
	}
}
