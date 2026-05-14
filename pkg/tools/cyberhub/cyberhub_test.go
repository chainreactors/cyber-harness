package cyberhub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/tools/resources"
	"github.com/chainreactors/fingers/common"
	fingerslib "github.com/chainreactors/fingers/fingers"
	"github.com/chainreactors/neutron/templates"
	sdkfingers "github.com/chainreactors/sdk/fingers"
	sdkneutron "github.com/chainreactors/sdk/neutron"
)

func TestCyberhubSearchesFingerprints(t *testing.T) {
	cmd := newTestCommand()

	out, err := cmd.Execute(context.Background(), []string{"search", "finger", "nginx"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, "[cyberhub] finger nginx http focus active 1") {
		t.Fatalf("output missing nginx fingerprint: %q", out)
	}
	if strings.Contains(out, "spring-rce") {
		t.Fatalf("finger search included poc: %q", out)
	}
	if !strings.Contains(out, "[cyberhub] search finger 1 1") {
		t.Fatalf("output missing summary: %q", out)
	}
}

func TestCyberhubListsPOCsWithFilters(t *testing.T) {
	cmd := newTestCommand()

	out, err := cmd.Execute(context.Background(), []string{"list", "poc", "--severity", "critical,high", "--finger", "spring", "--limit", "0"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, "spring-rce critical spring") {
		t.Fatalf("output missing spring poc: %q", out)
	}
	if strings.Contains(out, "tomcat-leak") {
		t.Fatalf("poc filter included tomcat: %q", out)
	}
}

func TestCyberhubSearchJSONLines(t *testing.T) {
	cmd := newTestCommand()

	out, err := cmd.Execute(context.Background(), []string{"search", "poc", "spring", "--json"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1: %q", len(lines), out)
	}
	var got item
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("json unmarshal error = %v", err)
	}
	if got.Kind != typePOC || got.ID != "spring-rce" || got.Severity != "critical" {
		t.Fatalf("json item = %#v", got)
	}
}

func newTestCommand() *Command {
	fingerCfg := sdkfingers.NewConfig().WithFingers(fingerslib.Fingers{
		{
			Name:        "nginx",
			Protocol:    "http",
			Tags:        []string{"web", "server"},
			Focus:       true,
			IsActive:    true,
			Level:       1,
			Description: "nginx web server",
			Attributes: common.Attributes{
				Vendor:  "nginx",
				Product: "nginx",
			},
		},
		{
			Name:        "redis",
			Protocol:    "tcp",
			Tags:        []string{"database"},
			Description: "redis service",
		},
	})
	neutronCfg := sdkneutron.NewConfig().WithTemplates([]*templates.Template{
		{
			Id:      "spring-rce",
			Fingers: []string{"spring"},
			Info: templates.Info{
				Name:        "Spring RCE",
				Severity:    "critical",
				Tags:        "spring,rce",
				Description: "spring remote code execution",
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
	})
	return New(&resources.Set{
		FingersConfig: fingerCfg,
		NeutronConfig: neutronCfg,
	})
}
