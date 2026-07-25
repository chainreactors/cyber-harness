package search

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/fingers/common"
	fingerslib "github.com/chainreactors/fingers/fingers"
	"github.com/chainreactors/neutron/templates"
	"github.com/chainreactors/sdk/pkg/association"
)

func runCyberhub(t *testing.T, cmd *CyberhubSearch, args ...string) string {
	t.Helper()
	var output bytes.Buffer
	if _, err := cmd.Run(context.Background(), &commands.Execution{Args: args, Stdout: &output, Stderr: &output}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return output.String()
}

func TestCyberhubSearchesFingerprints(t *testing.T) {
	cmd := newTestCyberhub()

	out := runCyberhub(t, cmd, "search", "finger", "nginx")
	if !strings.Contains(out, "nginx") {
		t.Fatalf("output missing nginx fingerprint: %q", out)
	}
	if strings.Contains(out, "spring-rce") {
		t.Fatalf("finger search included poc: %q", out)
	}
}

func TestCyberhubListsPOCsWithFilters(t *testing.T) {
	cmd := newTestCyberhub()

	out := runCyberhub(t, cmd, "list", "poc", "--severity", "critical,high", "--limit", "0")
	if !strings.Contains(out, "spring-rce") {
		t.Fatalf("output missing spring poc: %q", out)
	}
	if strings.Contains(out, "tomcat-leak") {
		t.Fatalf("poc filter included low severity tomcat: %q", out)
	}
}

func TestCyberhubSearchJSONLines(t *testing.T) {
	cmd := newTestCyberhub()

	out := runCyberhub(t, cmd, "search", "poc", "spring", "--json")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1: %q", len(lines), out)
	}
	var got cyberhubItem
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("json unmarshal error = %v", err)
	}
	if got.Kind != typePOC || got.ID != "spring-rce" || got.Severity != "critical" {
		t.Fatalf("json item = %#v", got)
	}
}

func TestCyberhubFingerAssociation(t *testing.T) {
	cmd := newTestCyberhub()

	out := runCyberhub(t, cmd, "search", "--finger", "spring")
	if !strings.Contains(out, "spring-rce") {
		t.Fatalf("--finger spring should find associated poc: %q", out)
	}
}

func TestCyberhubID(t *testing.T) {
	cmd := newTestCyberhub()

	out := runCyberhub(t, cmd, "id", "nginx")
	if !strings.Contains(out, "nginx") {
		t.Fatalf("id nginx should return nginx detail: %q", out)
	}
}

func newTestCyberhub() *CyberhubSearch {
	idx := association.NewIndex()
	idx.BuildWithFingers(
		fingerslib.Fingers{
			{
				Name:     "nginx",
				Protocol: "http",
				Tags:     []string{"web", "server"},
				Focus:    true,
				IsActive: true,
				Level:    1,
				Attributes: common.Attributes{
					Vendor:  "nginx",
					Product: "nginx",
				},
			},
			{
				Name:     "spring",
				Protocol: "http",
				Tags:     []string{"framework"},
				Focus:    true,
				Attributes: common.Attributes{
					Vendor:  "pivotal",
					Product: "spring",
				},
			},
			{
				Name:     "redis",
				Protocol: "tcp",
				Tags:     []string{"database"},
			},
		},
		nil,
		[]*templates.Template{
			{
				Id:      "spring-rce",
				Fingers: []string{"spring"},
				Info: templates.Info{
					Name:     "Spring RCE",
					Severity: "critical",
					Tags:     "spring,rce",
				},
			},
			{
				Id:      "tomcat-leak",
				Fingers: []string{"tomcat"},
				Info: templates.Info{
					Name:     "Tomcat Leak",
					Severity: "low",
					Tags:     "tomcat,exposure",
				},
			},
		},
	)
	return NewCyberhubSearch(idx)
}
