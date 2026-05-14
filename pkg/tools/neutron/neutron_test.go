package neutron

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/neutron/operators"
	neutronhttp "github.com/chainreactors/neutron/protocols/http"
	"github.com/chainreactors/neutron/templates"
	sdkneutron "github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/association"
)

func TestNormalizeNucleiStyleArgs(t *testing.T) {
	got := normalizeNucleiStyleArgs([]string{
		"-u", "http://127.0.0.1",
		"-tags", "cve,rce",
		"-severity=high,critical",
		"-rl", "20",
		"-eid", "skip-me",
	})
	want := []string{
		"-u", "http://127.0.0.1",
		"--tags", "cve,rce",
		"--severity=high,critical",
		"--rate-limit", "20",
		"--exclude-id", "skip-me",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("normalizeNucleiStyleArgs() = %#v, want %#v", got, want)
	}
}

func TestSelectNeutronTemplatesFiltersByCommonMetadata(t *testing.T) {
	engine := newTestNeutronEngine(t,
		testTemplate("critical-cve", "critical", "cve,rce", "nginx"),
		testTemplate("low-info", "low", "info", "php"),
		testTemplate("high-cve", "high", "cve", "nginx"),
	)
	index := association.NewFingerPOCIndex()
	index.BuildFromTemplates(engine.Get())

	selected, filtered := selectNeutronTemplates(engine, index, neutronExecuteOptions{
		Fingers:           []string{"nginx"},
		Tags:              []string{"cve"},
		Severities:        []string{"critical", "high"},
		ExcludeSeverities: []string{"high"},
	})
	if !filtered {
		t.Fatal("filtered = false, want true")
	}
	if len(selected) != 1 || selected[0].Id != "critical-cve" {
		t.Fatalf("selected = %#v, want only critical-cve", templateIDs(selected))
	}
}

func TestCommandTemplateListSupportsNucleiStyleFlagsAndJSON(t *testing.T) {
	cmd := New(newTestNeutronEngine(t,
		testTemplate("critical-cve", "critical", "cve,rce", "nginx"),
		testTemplate("low-info", "low", "info", "php"),
	), nil)

	out, err := cmd.Execute(context.Background(), []string{"-tl", "-severity", "critical", "-tags", "cve", "-j"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var finding neutronFinding
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &finding); err != nil {
		t.Fatalf("json output = %q, error = %v", out, err)
	}
	if finding.Template != "critical-cve" || finding.Severity != "critical" {
		t.Fatalf("finding = %#v", finding)
	}
}

func TestCommandLoadsTemplatesFromPathAndCanRestrict(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "custom.yml")
	err := os.WriteFile(templatePath, []byte(`id: custom-poc
info:
  name: Custom POC
  severity: high
  tags: custom
http:
  - method: GET
    path:
      - '{{BaseURL}}'
    matchers:
      - type: word
        words:
          - definitely-not-present
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cmd := New(newTestNeutronEngine(t, testTemplate("embedded", "low", "embedded", "")), nil)
	out, err := cmd.Execute(context.Background(), []string{"--template-list", "-t", templatePath})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, "custom-poc") || strings.Contains(out, "embedded") {
		t.Fatalf("output = %q", out)
	}
}

func newTestNeutronEngine(t *testing.T, items ...*templates.Template) *sdkneutron.Engine {
	t.Helper()
	engine, err := sdkneutron.NewEngineWithTemplates((sdkneutron.Templates{}).Merge(items))
	if err != nil {
		t.Fatalf("NewEngineWithTemplates() error = %v", err)
	}
	return engine
}

func testTemplate(id, severity, tags string, fingers ...string) *templates.Template {
	return &templates.Template{
		Id:      id,
		Fingers: fingers,
		Info: templates.Info{
			Name:     id,
			Severity: severity,
			Tags:     tags,
		},
		RequestsHTTP: []*neutronhttp.Request{
			{
				Method: "GET",
				Path:   []string{"{{BaseURL}}"},
				Operators: operators.Operators{
					Matchers: []*operators.Matcher{
						{Type: "word", Words: []string{"definitely-not-present"}},
					},
				},
			},
		},
	}
}

func templateIDs(items []*templates.Template) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil {
			out = append(out, item.Id)
		}
	}
	return out
}
