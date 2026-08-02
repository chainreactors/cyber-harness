package web

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chainreactors/libcstx/go"
)

const defaultReportLang = "zh"

type scoReportFacts struct {
	ips        []*cstx.Ip
	ports      []*cstx.Port
	apps       []*cstx.App
	urls       []*cstx.Url
	frameworks []*cstx.Framework
	vulns      []*cstx.Vuln
	other      map[string]int
}

func buildMarkdownReport(target, mode string, rawNodes []json.RawMessage, lang string) string {
	facts := collectSCOReportFacts(rawNodes)
	if strings.EqualFold(lang, "en") {
		return renderSCOReportEN(target, mode, facts)
	}
	return renderSCOReportZH(target, mode, facts)
}

func collectSCOReportFacts(rawNodes []json.RawMessage) scoReportFacts {
	facts := scoReportFacts{other: make(map[string]int)}
	for _, raw := range rawNodes {
		node, err := cstx.ParseSCONode(raw)
		if err != nil || node == nil {
			continue
		}
		switch value := node.(type) {
		case *cstx.Ip:
			facts.ips = append(facts.ips, value)
		case *cstx.Port:
			facts.ports = append(facts.ports, value)
		case *cstx.App:
			facts.apps = append(facts.apps, value)
		case *cstx.Url:
			facts.urls = append(facts.urls, value)
		case *cstx.Framework:
			facts.frameworks = append(facts.frameworks, value)
		case *cstx.Vuln:
			facts.vulns = append(facts.vulns, value)
		default:
			facts.other[node.CstxType()]++
		}
	}
	sort.Slice(facts.ips, func(i, j int) bool { return facts.ips[i].CstxID() < facts.ips[j].CstxID() })
	sort.Slice(facts.ports, func(i, j int) bool { return facts.ports[i].CstxID() < facts.ports[j].CstxID() })
	sort.Slice(facts.apps, func(i, j int) bool { return facts.apps[i].CstxID() < facts.apps[j].CstxID() })
	sort.Slice(facts.urls, func(i, j int) bool { return facts.urls[i].CstxID() < facts.urls[j].CstxID() })
	sort.Slice(facts.frameworks, func(i, j int) bool { return facts.frameworks[i].CstxID() < facts.frameworks[j].CstxID() })
	sort.Slice(facts.vulns, func(i, j int) bool { return facts.vulns[i].CstxID() < facts.vulns[j].CstxID() })
	return facts
}

func renderSCOReportZH(target, mode string, facts scoReportFacts) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# 扫描报告\n\n- 目标：`%s`\n- 模式：%s\n\n", markdownInline(target), scanModeLabel(mode, false))
	writeSCOOverview(&out, facts, false)
	writeSCOSections(&out, facts, false)
	return out.String()
}

func renderSCOReportEN(target, mode string, facts scoReportFacts) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Scan Report\n\n- Target: `%s`\n- Mode: %s\n\n", markdownInline(target), scanModeLabel(mode, true))
	writeSCOOverview(&out, facts, true)
	writeSCOSections(&out, facts, true)
	return out.String()
}

func writeSCOOverview(out *strings.Builder, facts scoReportFacts, english bool) {
	title, typeLabel, countLabel := "## 概览", "类型", "数量"
	labels := []string{"IP", "端口", "应用", "URL", "框架", "漏洞"}
	if english {
		title, typeLabel, countLabel = "## Overview", "Type", "Count"
		labels = []string{"IP", "Port", "App", "URL", "Framework", "Vulnerability"}
	}
	fmt.Fprintf(out, "%s\n\n| %s | %s |\n|---|---:|\n", title, typeLabel, countLabel)
	counts := []int{len(facts.ips), len(facts.ports), len(facts.apps), len(facts.urls), len(facts.frameworks), len(facts.vulns)}
	for i, label := range labels {
		fmt.Fprintf(out, "| %s | %d |\n", label, counts[i])
	}
	otherTypes := make([]string, 0, len(facts.other))
	for nodeType := range facts.other {
		otherTypes = append(otherTypes, nodeType)
	}
	sort.Strings(otherTypes)
	for _, nodeType := range otherTypes {
		fmt.Fprintf(out, "| `%s` | %d |\n", markdownInline(nodeType), facts.other[nodeType])
	}
	out.WriteString("\n")
}

func writeSCOSections(out *strings.Builder, facts scoReportFacts, english bool) {
	if len(facts.ips)+len(facts.ports)+len(facts.apps)+len(facts.urls)+len(facts.frameworks)+len(facts.vulns) == 0 && len(facts.other) == 0 {
		if english {
			out.WriteString("No SCO facts were emitted.\n")
		} else {
			out.WriteString("本次扫描未产生 SCO 事实。\n")
		}
		return
	}
	if len(facts.ips) > 0 {
		writeSectionTitle(out, "IP", "IP", english)
		for _, value := range facts.ips {
			fmt.Fprintf(out, "- `%s`\n", markdownInline(value.Ip))
		}
		out.WriteString("\n")
	}
	if len(facts.ports) > 0 {
		writeSectionTitle(out, "端口", "Ports", english)
		for _, value := range facts.ports {
			fmt.Fprintf(out, "- `%s:%s/%s`\n", markdownInline(value.Ip), markdownInline(value.Port), markdownInline(value.Protocol))
		}
		out.WriteString("\n")
	}
	if len(facts.apps) > 0 {
		writeSectionTitle(out, "应用", "Applications", english)
		for _, value := range facts.apps {
			label := firstReportValue(value.Title, value.AppId, value.Url, value.CstxID())
			fmt.Fprintf(out, "- **%s**", markdownInline(label))
			if value.Url != "" {
				fmt.Fprintf(out, " — `%s`", markdownInline(value.Url))
			}
			if value.StatusCode != 0 {
				fmt.Fprintf(out, " — HTTP %d", value.StatusCode)
			}
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}
	if len(facts.urls) > 0 {
		writeSectionTitle(out, "WEB", "Web", english)
		for _, value := range facts.urls {
			url := value.Scheme + "://" + value.Host
			if value.Port != "" {
				url += ":" + value.Port
			}
			url += value.Path
			fmt.Fprintf(out, "- `%s`", markdownInline(url))
			if value.StatusCode != 0 {
				fmt.Fprintf(out, " — HTTP %d", value.StatusCode)
			}
			if value.Title != "" {
				fmt.Fprintf(out, " — %s", markdownInline(value.Title))
			}
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}
	if len(facts.frameworks) > 0 {
		writeSectionTitle(out, "框架", "Frameworks", english)
		for _, value := range facts.frameworks {
			name := firstReportValue(value.Name, value.Product, value.CstxID())
			if value.Version != "" {
				name += " " + value.Version
			}
			fmt.Fprintf(out, "- %s\n", markdownInline(name))
		}
		out.WriteString("\n")
	}
	if len(facts.vulns) > 0 {
		writeSectionTitle(out, "漏洞", "Vulnerabilities", english)
		for _, value := range facts.vulns {
			name := firstReportValue(value.Name, value.VulnId, value.Value, value.CstxID())
			fmt.Fprintf(out, "- **%s**", markdownInline(name))
			if value.Severity != "" {
				fmt.Fprintf(out, " — `%s`", markdownInline(value.Severity))
			}
			if value.Url != "" {
				fmt.Fprintf(out, " — `%s`", markdownInline(value.Url))
			}
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}
}

func writeSectionTitle(out *strings.Builder, zh, en string, english bool) {
	if english {
		fmt.Fprintf(out, "## %s\n\n", en)
		return
	}
	fmt.Fprintf(out, "## %s\n\n", zh)
}

func scanModeLabel(mode string, english bool) string {
	if english {
		switch mode {
		case "quick":
			return "Quick"
		case "full":
			return "Full"
		default:
			return markdownInline(mode)
		}
	}
	switch mode {
	case "quick":
		return "快速"
	case "full":
		return "完整"
	default:
		return markdownInline(mode)
	}
}

func firstReportValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "-"
}

func markdownInline(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.TrimSpace(value)
}
