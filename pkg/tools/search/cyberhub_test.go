package search

import (
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

func TestCyberhubSearchesFingerprints(t *testing.T) {
	cmd := newTestCyberhub()

	commands.Output.Reset(nil)
	err := cmd.Execute(context.Background(), []string{"search", "finger", "nginx"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := commands.Output.Captured()
	if !strings.Contains(out, "nginx") {
		t.Fatalf("output missing nginx fingerprint: %q", out)
	}
	if strings.Contains(out, "spring-rce") {
		t.Fatalf("finger search included poc: %q", out)
	}
}

func TestCyberhubListsPOCsWithFilters(t *testing.T) {
	cmd := newTestCyberhub()

	commands.Output.Reset(nil)
	err := cmd.Execute(context.Background(), []string{"list", "poc", "--severity", "critical,high", "--limit", "0"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := commands.Output.Captured()
	if !strings.Contains(out, "spring-rce") {
		t.Fatalf("output missing spring poc: %q", out)
	}
	if strings.Contains(out, "tomcat-leak") {
		t.Fatalf("poc filter included low severity tomcat: %q", out)
	}
}

func TestCyberhubSearchJSONLines(t *testing.T) {
	cmd := newTestCyberhub()

	commands.Output.Reset(nil)
	err := cmd.Execute(context.Background(), []string{"search", "poc", "spring", "--json"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := commands.Output.Captured()
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

	commands.Output.Reset(nil)
	err := cmd.Execute(context.Background(), []string{"search", "--finger", "spring"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := commands.Output.Captured()
	if !strings.Contains(out, "spring-rce") {
		t.Fatalf("--finger spring should find associated poc: %q", out)
	}
}

func TestCyberhubID(t *testing.T) {
	cmd := newTestCyberhub()

	commands.Output.Reset(nil)
	err := cmd.Execute(context.Background(), []string{"id", "nginx"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	out := commands.Output.Captured()
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

