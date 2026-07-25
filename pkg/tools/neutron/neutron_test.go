package neutron

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/commands"
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

func TestUsageIsGeneratedFromNeutronFlags(t *testing.T) {
	usage := New(nil, nil).Usage()
	if !strings.Contains(usage, "Usage:") || !strings.Contains(usage, "neutron [OPTIONS]") {
		t.Fatalf("usage was not rendered by the neutron go-flags parser:\n%s", usage)
	}
	for _, flag := range []string{"target", "templates", "rate-limit", "restrict-templates"} {
		if !strings.Contains(usage, "--"+flag) && !strings.Contains(usage, "/"+flag) {
			t.Fatalf("usage missing generated flag %q:\n%s", flag, usage)
		}
	}
}

func TestSelectNeutronTemplatesFiltersByCommonMetadata(t *testing.T) {
	engine := newTestNeutronEngine(t,
		testTemplate("critical-cve", "critical", "cve,rce", "nginx"),
		testTemplate("low-info", "low", "info", "php"),
		testTemplate("high-cve", "high", "cve", "nginx"),
	)
	index := association.NewIndex()
	index.Build(nil, engine.Get())

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

	var output bytes.Buffer
	_, err := cmd.Run(context.Background(), &commands.Execution{Args: []string{"-tl", "-severity", "critical", "-tags", "cve", "-j"}, Stdout: &output, Stderr: &output})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := output.String()
	var result neutronResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Fatalf("json output = %q, error = %v", out, err)
	}
	if result.Template != "critical-cve" || result.Severity != "critical" {
		t.Fatalf("result = %#v", result)
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
	var output bytes.Buffer
	_, err = cmd.Run(context.Background(), &commands.Execution{Args: []string{"--template-list", "-t", templatePath}, Stdout: &output, Stderr: &output})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := output.String()
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
