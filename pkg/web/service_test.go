package web

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/utils/parsers"
)

func TestScanRequestFields(t *testing.T) {
	req := ScanRequest{Verify: true, Deep: true}
	if !req.Verify || req.Sniper || !req.Deep {
		t.Fatalf("scan request = verify:%v sniper:%v deep:%v", req.Verify, req.Sniper, req.Deep)
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

func TestGetScanRebuildsLegacyMergedAssets(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "scans.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	now := time.Now()
	job := &ScanJob{
		ID:        "legacy-merged-services",
		Target:    "111.63.65.103",
		Mode:      "quick",
		Status:    StatusCompleted,
		CreatedAt: now,
		UpdatedAt: now,
		Result: &output.Result{
			Services: []*parsers.GOGOResult{
				{Ip: "111.63.65.103", Port: "80", Protocol: "http"},
				{Ip: "111.63.65.103", Port: "443", Protocol: "https"},
				{Ip: "111.63.65.103", Port: "icmp", Protocol: "icmp"},
			},
			WebProbes: []*parsers.SprayResult{
				{UrlString: "http://111.63.65.103/", Status: 200, Source: parsers.CheckSource},
				{UrlString: "https://111.63.65.103/", Status: 301, Source: parsers.CheckSource},
			},
			Assets: []output.Asset{{
				Target: "https://111.63.65.103",
				Items: []output.AssetItem{{
					Kind: output.AssetItemResponse, Target: "https://111.63.65.103", Summary: "saved analysis",
				}},
			}},
		},
	}
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := NewService(ServiceConfig{Store: store}).GetScan(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("GetScan() error = %v", err)
	}
	if len(got.Result.Assets) != 3 {
		t.Fatalf("assets = %d, want 3 separated services: %#v", len(got.Result.Assets), got.Result.Assets)
	}
	foundAnalysis := false
	for _, asset := range got.Result.Assets {
		for _, item := range asset.Items {
			foundAnalysis = foundAnalysis || item.Kind == output.AssetItemResponse && item.Summary == "saved analysis"
		}
	}
	if !foundAnalysis {
		t.Fatal("supplemental analysis item was dropped during legacy asset rebuild")
	}
}

func TestGetScanRebuildsLegacyWebOnlyAssets(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "scans.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer store.Close()

	now := time.Now()
	job := &ScanJob{
		ID:        "legacy-web-only",
		Target:    "111.63.65.103",
		Mode:      "quick",
		Status:    StatusCompleted,
		CreatedAt: now,
		UpdatedAt: now,
		Result: &output.Result{
			WebProbes: []*parsers.SprayResult{
				{UrlString: "http://111.63.65.103/", Status: 200, Source: parsers.CheckSource},
				{UrlString: "https://111.63.65.103/", Status: 301, Source: parsers.CheckSource},
			},
			Assets: []output.Asset{{Target: "http://111.63.65.103"}},
		},
	}
	if err := store.Create(context.Background(), job); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := NewService(ServiceConfig{Store: store}).GetScan(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("GetScan() error = %v", err)
	}
	if len(got.Result.Assets) != 2 {
		t.Fatalf("assets = %d, want separate http and https web origins: %#v", len(got.Result.Assets), got.Result.Assets)
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
	}, "en")

	for _, want := range []string{"## Evidence Analysis", "| Asset | Details |"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
}

func TestBuildMarkdownReportLocalizedAndDeNoised(t *testing.T) {
	result := &output.Result{
		Summary: output.Summary{Targets: 1, Services: 3, Webs: 2, Probes: 2, Duration: "22.266s"},
		Assets: []output.Asset{
			{
				Target: "http://111.63.65.103:80",
				Title:  "BWS/1.1",
				Items: []output.AssetItem{
					{Kind: output.AssetItemService, Source: "gogo_portscan", Data: map[string]any{"service": "http", "port": "80"}},
					{Kind: output.AssetItemPath, Status: "301"},
					{Kind: output.AssetItemPath, Status: "200"},
				},
			},
			{
				// Bare live host — only an icmp echo. Must fold into the trailing
				// list, not claim its own ### section, and must not inflate the host count.
				Target: "111.63.65.103:icmp",
				Key:    "111.63.65.103:icmp",
				Items: []output.AssetItem{
					{Kind: output.AssetItemService, Source: "gogo_portscan", Data: map[string]any{"service": "icmp"}},
				},
			},
		},
	}

	zh := buildMarkdownReport("baidu.com", "quick", result, "zh")
	for _, want := range []string{"# 侦察报告", "## 概述", "快速侦察", "1 台主机", "其他存活主机"} {
		if !strings.Contains(zh, want) {
			t.Fatalf("zh report missing %q:\n%s", want, zh)
		}
	}
	// The "去 AI 味" contract: no internal scanner names leak, no generic English boilerplate title.
	if strings.Contains(zh, "gogo_portscan") {
		t.Errorf("zh report leaks scanner source name:\n%s", zh)
	}
	if strings.Contains(zh, "战利品") {
		t.Errorf("zh report leaks internal loot terminology:\n%s", zh)
	}
	if strings.Contains(zh, "Penetration Test Report") || strings.Contains(zh, "| Metric | Value |") {
		t.Errorf("zh report still uses the old boilerplate:\n%s", zh)
	}
	// icmp is folded, so it must not appear as its own heading.
	if strings.Contains(zh, "### 111.63.65.103:icmp") {
		t.Errorf("bare icmp host got its own section:\n%s", zh)
	}

	en := buildMarkdownReport("baidu.com", "quick", result, "en")
	for _, want := range []string{"## Overview", "Quick recon", "1 host", "Other live hosts"} {
		if !strings.Contains(en, want) {
			t.Fatalf("en report missing %q:\n%s", want, en)
		}
	}
	if strings.Contains(en, "gogo_portscan") {
		t.Errorf("en report leaks scanner source name:\n%s", en)
	}
	if strings.Contains(strings.ToLower(en), "loot") {
		t.Errorf("en report leaks internal loot terminology:\n%s", en)
	}
}
