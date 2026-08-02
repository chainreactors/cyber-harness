package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildMarkdownReportUsesLibcstxFacts(t *testing.T) {
	nodes := []json.RawMessage{
		json.RawMessage(`{"cstx_type":"ip","cstx_id":"ip:111.63.65.103","ip":"111.63.65.103"}`),
		json.RawMessage(`{"cstx_type":"port","cstx_id":"port:111.63.65.103:80:tcp","ip":"111.63.65.103","port":"80","protocol":"tcp"}`),
		json.RawMessage(`{"cstx_type":"url","cstx_id":"url:http://111.63.65.103/","scheme":"http","host":"111.63.65.103","path":"/","status_code":200,"title":"BWS/1.1"}`),
		json.RawMessage(`{"cstx_type":"framework","cstx_id":"framework:bws","name":"BWS","version":"1.1"}`),
		json.RawMessage(`{"cstx_type":"vuln","cstx_id":"vuln:test","value":"CVE-TEST","name":"Example finding","severity":"high","url":"http://111.63.65.103/"}`),
	}

	zh := BuildMarkdownReport("baidu.com", "quick", nodes, "zh")
	for _, want := range []string{"# 扫描报告", "## 概览", "快速", "## 端口", "## WEB", "## 框架", "## 漏洞"} {
		if !strings.Contains(zh, want) {
			t.Fatalf("zh report missing %q:\n%s", want, zh)
		}
	}
	for _, old := range []string{"Asset", "Service", "WebProbe", "Loot"} {
		if strings.Contains(zh, old) {
			t.Fatalf("report leaked removed AIScan taxonomy %q:\n%s", old, zh)
		}
	}

	en := BuildMarkdownReport("baidu.com", "quick", nodes, "en")
	for _, want := range []string{"# Scan Report", "## Overview", "Quick", "## Ports", "## Web", "## Frameworks", "## Vulnerabilities"} {
		if !strings.Contains(en, want) {
			t.Fatalf("en report missing %q:\n%s", want, en)
		}
	}
}
