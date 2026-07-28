package output

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// updateReportGolden rewrites the .golden files instead of comparing against
// them, so the diff of a deliberate rendering change is reviewable on its own.
var updateReportGolden = flag.Bool("update-report-golden", false, "rewrite report golden files")

// LoadReportFixture reads one of the shared report fixtures. It lives in
// core/output because that is where the fixtures live, but pkg/web reads the
// same files so the two renderers are pinned against identical input.
func loadReportFixture(t *testing.T, name string) *Result {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	result := &Result{}
	if err := json.Unmarshal(raw, result); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return result
}

// reportStamp matches the "generated at" header timestamp, the one part of the
// markdown report that cannot be pinned.
var reportStamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)

func checkReportGolden(t *testing.T, name, got string) {
	t.Helper()
	got = reportStamp.ReplaceAllString(got, "<timestamp>")
	path := filepath.Join("testdata", name+".golden")
	if *updateReportGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run go test -run %s -update-report-golden): %v", t.Name(), err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

// TestAssetReportGolden pins the terminal asset report. FormatAssetReport is a
// wrapper over RenderReport, so this is also the ANSI emitter's contract.
func TestAssetReportGolden(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fixture string
		color   bool
	}{
		{name: "asset_plain", fixture: "report_fixture"},
		{name: "asset_color", fixture: "report_fixture", color: true},
		{name: "asset_empty", fixture: "report_empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkReportGolden(t, tc.name, FormatAssetReport(loadReportFixture(t, tc.fixture), tc.color))
		})
	}
}

// TestRenderReportMarkdownGolden pins the markdown emitter under the option
// sets its two callers use. The web_* goldens must stay byte-identical to
// pkg/web/testdata/web_*.golden — that is the cross-package parity check.
func TestRenderReportMarkdownGolden(t *testing.T) {
	web := ReportOptions{Style: StyleMarkdown, Title: "10.0.0.1", Mode: "quick", CollapseBare: true}
	tool := ReportOptions{Style: StyleMarkdown, Title: "Scan Report", Sitemap: true, CollapseBare: true, Metrics: true, Inventory: true}

	for _, tc := range []struct {
		name    string
		fixture string
		opts    ReportOptions
		nilRes  bool
	}{
		{name: "md_web_zh", fixture: "report_fixture", opts: withLang(web, "zh")},
		{name: "md_web_en", fixture: "report_fixture", opts: withLang(web, "en")},
		{name: "md_web_empty_zh", fixture: "report_empty", opts: withLang(web, "zh")},
		{name: "md_web_empty_en", fixture: "report_empty", opts: withLang(web, "en")},
		{name: "md_web_nil", opts: withLang(web, "en"), nilRes: true},
		{name: "md_tool", fixture: "report_fixture", opts: tool},
		{name: "md_tool_empty", fixture: "report_empty", opts: tool},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var result *Result
			if !tc.nilRes {
				result = loadReportFixture(t, tc.fixture)
			}
			checkReportGolden(t, tc.name, RenderReport(result, tc.opts))
		})
	}
}

func withLang(opts ReportOptions, lang string) ReportOptions {
	opts.Lang = lang
	return opts
}

func TestAssetReportNilResult(t *testing.T) {
	if got := FormatAssetReport(nil, false); got != "Assets: 0 total\n" {
		t.Fatalf("nil result = %q", got)
	}
}
