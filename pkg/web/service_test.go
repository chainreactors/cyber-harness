package web

import (
	"reflect"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/core/output"
)

func TestScanRequestAnalysisOptions(t *testing.T) {
	verify, sniper, deep := ScanRequest{Verify: true, Deep: true}.AnalysisOptions()
	if !verify || sniper || !deep {
		t.Fatalf("new analysis options = verify:%v sniper:%v deep:%v", verify, sniper, deep)
	}

	verify, sniper, deep = ScanRequest{AI: true}.AnalysisOptions()
	if !verify || !sniper || deep {
		t.Fatalf("legacy AI options = verify:%v sniper:%v deep:%v", verify, sniper, deep)
	}
}

func TestScanArgsForSelectedAnalysisOptions(t *testing.T) {
	job := &ScanJob{
		Target: "127.0.0.1",
		Mode:   "full",
		Verify: true,
		Sniper: true,
		Deep:   true,
	}

	got := scanArgsForJob(job)
	want := []string{"-i", "127.0.0.1", "--mode", "full", "--verify=high", "--sniper", "--deep"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scan args = %#v, want %#v", got, want)
	}
}

func TestServiceStatusReportsLLMAvailability(t *testing.T) {
	service := NewService(ServiceConfig{})
	if service.Status().LLMAvailable {
		t.Fatal("LLMAvailable = true, want false without provider")
	}
}

func TestBuildMarkdownReportKeepsAssetDetailAsMarkdown(t *testing.T) {
	report := buildMarkdownReport("http://127.0.0.1:8092", "quick", &output.Result{
		Summary: output.Summary{Targets: 1},
		Assets: []output.Asset{
			{
				Target: "http://127.0.0.1:8092",
				Items: []output.AssetItem{
					{
						Kind:    output.AssetItemResponse,
						Source:  "deep",
						Status:  "response",
						Summary: "manual agent response",
						Detail:  "Let me analyze the collected browser evidence.\n\n## Evidence Analysis\n\n| Asset | Details |\n|---|---|\n| API | GET /api/scans |",
					},
				},
			},
		},
	})

	for _, want := range []string{"## Evidence Analysis", "| Asset | Details |"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}
