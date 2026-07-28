package output

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ReportStyle selects the emitter RenderReport hands the neutral model to.
type ReportStyle uint8

const (
	// StyleANSI is the operator-facing terminal report.
	StyleANSI ReportStyle = iota
	// StyleMarkdown is the report shipped to the web UI and to tool output.
	StyleMarkdown
)

// ReportOptions is the single knob set behind every asset report. The three
// renderers this replaced each owned a feature nobody else had (the sitemap
// tree, zh/en text + bare-host folding, the counter table), so the flags are
// what a caller opts into rather than what a style implies.
type ReportOptions struct {
	Style ReportStyle
	// Color enables ANSI escapes. StyleMarkdown ignores it — markdown output
	// is never colorized.
	Color bool
	// Lang is "zh" or "en" (anything else, including "", means "en").
	// StyleANSI is English-only, so it ignores this.
	Lang string
	// Title is the report subject: the scan target for a web job, or a plain
	// report name. Mode is the scan mode ("quick" / "full"); leaving Mode empty
	// suppresses the target/mode/timestamp line and makes Title the bare H1.
	// Markdown only.
	Title string
	Mode  string
	// Sitemap renders the per-asset path tree.
	Sitemap bool
	// CollapseBare folds live hosts that answered with nothing but non-web
	// services into a trailing list instead of giving each one a section.
	// Markdown only.
	CollapseBare bool
	// Metrics adds the counter table. Markdown only.
	Metrics bool
	// Inventory adds the flat per-kind sections (services / web evidence /
	// findings / errors) the scan tool report carries. Markdown only.
	Inventory bool
}

// RenderReport walks Result → Asset → AssetItem exactly once into a neutral
// model, then emits it in the requested style.
func RenderReport(result *Result, opts ReportOptions) string {
	model := buildReportModel(result, opts)
	var report string
	if opts.Style == StyleMarkdown {
		report = renderMarkdownReport(model, opts)
	} else {
		report = renderANSIReport(model, opts)
	}
	return strings.TrimRight(report, " \t\r\n") + "\n"
}

// --- neutral model ---

type reportModel struct {
	nilResult bool
	summary   Summary
	total     int
	hosts     int
	fingers   int
	assets    []reportAsset
	bare      []reportAsset
}

type reportAsset struct {
	title    string // Title > Target > Key — the headline
	label    string // Target > Title > Key — the bare-host list entry
	target   string
	status   string
	paths    int
	services []string
	statuses []string
	fingers  []string
	items    []reportItem
	sitemap  *sitemapNode
	isBare   bool
}

// reportItem is the per-item extraction — the part that used to exist in three
// places. text is the one-line rendering, name the short label used where a
// full line will not fit (sitemap annotations).
type reportItem struct {
	kind      string
	label     string // note-like items: Source > Kind
	status    string
	target    string
	text      string
	name      string
	detail    string
	length    int
	fingers   []string
	validated bool
}

func buildReportModel(result *Result, opts ReportOptions) reportModel {
	if result == nil {
		return reportModel{nilResult: true}
	}
	model := reportModel{summary: result.Summary, total: len(result.Assets)}

	hosts := make(map[string]struct{})
	fingers := make(map[string]struct{})
	for _, asset := range result.Assets {
		item := buildReportAsset(asset, opts.Sitemap)
		if host := reportAssetHost(asset); host != "" {
			hosts[host] = struct{}{}
		}
		for _, finger := range item.fingers {
			fingers[strings.ToLower(finger)] = struct{}{}
		}
		if opts.CollapseBare && item.isBare {
			model.bare = append(model.bare, item)
			continue
		}
		model.assets = append(model.assets, item)
	}

	// An asset whose target parses to nothing still counts as a host.
	model.hosts = len(hosts)
	if model.hosts == 0 {
		model.hosts = len(result.Assets)
	}
	model.fingers = len(fingers)
	return model
}

func buildReportAsset(asset Asset, sitemap bool) reportAsset {
	out := reportAsset{
		title:  FirstNonEmpty(asset.Title, asset.Target, asset.Key),
		label:  FirstNonEmpty(asset.Target, asset.Title, asset.Key),
		target: asset.Target,
		status: asset.Status,
	}

	var services, statuses, fingers []string
	annotations := make(map[string][]string)
	hasService, onlyPlainServices := false, true

	for _, item := range asset.Items {
		entry := reportItem{kind: item.Kind, status: item.Status, target: item.Target}
		switch item.Kind {
		case AssetItemService:
			hasService = true
			facts := strings.Join(CompactStrings(
				AssetDataString(item.Data, "protocol"),
				AssetDataString(item.Data, "service"),
				AssetDataString(item.Data, "port"),
			), " ")
			services = append(services, facts)
			// A service with no structured facts still has a name to show.
			entry.text = FirstNonEmpty(facts, item.Title, item.Target, item.Raw)
			if isWebServiceItem(item) {
				onlyPlainServices = false
			}
		case AssetItemFingerprint:
			onlyPlainServices = false
			entry.text = FirstNonEmpty(item.Title, item.Summary, AssetDataString(item.Data, "name"), item.Target)
			entry.name = entry.text
			fingers = append(fingers, entry.text)
			if path := pathFromTarget(item.Target, asset.Target); path != "" {
				annotations[path] = appendUniq(annotations[path], entry.text)
			}
		case AssetItemPath:
			onlyPlainServices = false
			out.paths++
			entry.text = FirstNonEmpty(AssetDataString(item.Data, "path"), WebPath(item.Target), item.Target)
			entry.name = item.Title
			entry.length = AssetDataInt(item.Data, "length")
			entry.fingers = AssetDataStrings(item.Data, "fingers")
			entry.validated = HasTag(item.Tags, "validated")
			fingers = append(fingers, entry.fingers...)
			if item.Status != "" {
				statuses = append(statuses, item.Status)
			}
		case AssetItemLoot, AssetItemNote, AssetItemResponse, AssetItemError:
			onlyPlainServices = false
			entry.label = FirstNonEmpty(item.Source, item.Kind)
			entry.detail = AssetItemDetail(item)
			entry.text = FirstNonEmpty(item.Summary, item.Title, firstContentLine(entry.detail), item.Raw)
			entry.name = FirstNonEmpty(item.Title, item.Summary)
			if item.Kind != AssetItemError {
				path := lootAnnotationPath(item, asset.Target)
				annotations[path] = appendUniq(annotations[path], lootAnnotation(entry))
			}
		default:
			onlyPlainServices = false
			entry.text = FirstNonEmpty(item.Summary, item.Title, item.Raw)
		}
		out.items = append(out.items, entry)
	}

	out.isBare = hasService && onlyPlainServices
	out.services = CompactStrings(services...)
	out.statuses = CompactStrings(statuses...)
	out.fingers = CompactStrings(fingers...)
	if sitemap {
		out.sitemap = buildSitemapTree(out.items, annotations)
	}
	return out
}

// isWebServiceItem reports whether a service item is an HTTP-ish one, which is
// what keeps its host out of the "bare live host" bucket.
func isWebServiceItem(item AssetItem) bool {
	svc := strings.ToLower(AssetDataString(item.Data, "service") + " " + AssetDataString(item.Data, "protocol"))
	return strings.Contains(svc, "http")
}

func lootAnnotationPath(item AssetItem, assetTarget string) string {
	if path := pathFromTarget(item.Target, assetTarget); path != "" {
		return path
	}
	return "/"
}

// lootAnnotation is the compact "{skill:status summary}" tag hung off a
// sitemap node. Long summaries are dropped rather than wrapped.
func lootAnnotation(entry reportItem) string {
	label := entry.label
	if entry.status != "" {
		label += ":" + entry.status
	}
	if entry.name != "" && len(entry.name) <= 40 {
		label += " " + entry.name
	}
	return label
}

// reportAssetHost reduces an asset to its host, so an IP that answered on both
// icmp and http counts once.
func reportAssetHost(asset Asset) string {
	value := FirstNonEmpty(asset.Target, asset.Key, asset.Title)
	if i := strings.Index(value, "://"); i >= 0 {
		value = value[i+3:]
	}
	if i := strings.IndexAny(value, "/?#"); i >= 0 {
		value = value[:i]
	}
	if strings.Count(value, ":") == 1 { // host:port — drop the port, leave IPv6 alone
		value = value[:strings.LastIndex(value, ":")]
	}
	return value
}

// --- ANSI emitter ---

func renderANSIReport(model reportModel, opts ReportOptions) string {
	if model.nilResult {
		return "Assets: 0 total\n"
	}
	c := NewColor(opts.Color)

	var sb strings.Builder
	fmt.Fprintf(&sb, "Assets: %d total\n", model.total)
	fmt.Fprintf(&sb, "Summary: %d target(s), %d service(s), %d web endpoint(s), %d probe(s), %d loot(s), %d error(s), %s\n\n",
		model.summary.Targets,
		model.summary.Services,
		model.summary.Webs,
		model.summary.Probes,
		model.summary.Loots,
		model.summary.Errors,
		model.summary.Duration,
	)
	if model.total == 0 {
		return sb.String()
	}

	for i, asset := range model.assets {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, c.GreenBold(asset.title))
		if asset.target != "" && asset.target != asset.title {
			fmt.Fprintf(&sb, "   target: %s\n", asset.target)
		}
		if asset.status != "" {
			fmt.Fprintf(&sb, "   status: %s\n", asset.status)
		}
		for _, item := range asset.items {
			writeANSIItem(&sb, item, c)
		}
		if asset.sitemap != nil {
			sb.WriteString("   sitemap:\n")
			renderSitemapNode(&sb, asset.sitemap, "   ", true, c)
		}
		if i < len(model.assets)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func writeANSIItem(sb *strings.Builder, item reportItem, c Color) {
	switch item.kind {
	case AssetItemPath:
		return
	case AssetItemService:
		fmt.Fprintf(sb, "   %s %s\n", c.Cyan("service:"), item.text)
	case AssetItemFingerprint:
		fmt.Fprintf(sb, "   %s %s\n", c.Cyan("fingerprint:"), item.text)
	case AssetItemLoot, AssetItemNote, AssetItemResponse:
		line := item.text
		if item.status != "" {
			line = c.Yellow("["+item.status+"]") + " " + line
		}
		fmt.Fprintf(sb, "   %s %s\n", c.Yellow(item.label+":"), line)
		if item.detail != "" && item.detail != line && !strings.Contains(line, item.detail) {
			for _, detailLine := range strings.Split(strings.TrimSpace(item.detail), "\n") {
				if detailLine = strings.TrimSpace(detailLine); detailLine != "" {
					fmt.Fprintf(sb, "      %s\n", c.Dim(detailLine))
				}
			}
		}
	case AssetItemError:
		fmt.Fprintf(sb, "   %s %s\n", c.Red("error:"), item.text)
	}
}

// --- markdown emitter ---

// reportLang is the whole i18n surface: one flag, one lookup.
type reportLang struct{ zh bool }

func newReportLang(lang string) reportLang {
	return reportLang{zh: strings.HasPrefix(strings.ToLower(lang), "zh")}
}

func (t reportLang) tr(zh, en string) string {
	if t.zh {
		return zh
	}
	return en
}

func (t reportLang) sep() string { return t.tr("：", ": ") }

func (t reportLang) modeName(mode string) string {
	if strings.EqualFold(mode, "full") {
		return t.tr("全面侦察", "Full recon")
	}
	return t.tr("快速侦察", "Quick recon")
}

// renderMarkdownReport writes an operator-facing report: prose instead of a
// metric dump, no internal scanner names leaking into the text.
func renderMarkdownReport(model reportModel, opts ReportOptions) string {
	t := newReportLang(opts.Lang)

	var sb strings.Builder
	writeMarkdownHeader(&sb, t, opts)
	if model.nilResult {
		sb.WriteString(t.tr("本次扫描未返回结构化结果。\n", "No structured result was returned.\n"))
		return sb.String()
	}

	sb.WriteString("## " + t.tr("概述", "Overview") + "\n\n")
	var overview strings.Builder
	writeMarkdownOverview(&overview, t, model)
	sb.WriteString(strings.TrimSpace(overview.String()))
	sb.WriteString("\n\n")

	if opts.Metrics {
		writeMarkdownMetrics(&sb, t, model)
	}
	if len(model.assets) > 0 {
		sb.WriteString("## " + t.tr("资产明细", "Assets") + "\n\n")
		for _, asset := range model.assets {
			writeMarkdownAsset(&sb, t, asset, opts)
		}
	}
	if len(model.bare) > 0 {
		sb.WriteString("## " + t.tr("其他存活主机", "Other live hosts") + "\n\n")
		for _, asset := range model.bare {
			if len(asset.services) > 0 {
				fmt.Fprintf(&sb, "- `%s` · %s\n", asset.label, strings.Join(asset.services, ", "))
				continue
			}
			fmt.Fprintf(&sb, "- `%s`\n", asset.label)
		}
		sb.WriteString("\n")
	}
	if opts.Inventory {
		writeMarkdownInventory(&sb, t, model)
	}
	return sb.String()
}

func writeMarkdownHeader(sb *strings.Builder, t reportLang, opts ReportOptions) {
	if opts.Mode == "" {
		fmt.Fprintf(sb, "# %s\n\n", FirstNonEmpty(opts.Title, t.tr("侦察报告", "Recon report")))
		sb.WriteString("---\n\n")
		return
	}
	fmt.Fprintf(sb, "# %s%s\n\n", t.tr("侦察报告 · ", "Recon report · "), FirstNonEmpty(opts.Title, t.tr("目标", "target")))
	fmt.Fprintf(sb, "%s `%s`  ·  %s  ·  %s\n\n",
		t.tr("目标", "Target"), opts.Title,
		t.modeName(opts.Mode),
		time.Now().Format("2006-01-02 15:04:05"))
	sb.WriteString("---\n\n")
}

// writeMarkdownOverview is the executive summary — one flowing paragraph that
// names only the numbers actually present, so a clean scan reads like a
// sentence rather than a table full of zeros.
func writeMarkdownOverview(sb *strings.Builder, t reportLang, model reportModel) {
	s := model.summary
	if t.zh {
		fmt.Fprintf(sb, "本次侦察共识别 %d 台主机、%d 个开放服务", model.hosts, s.Services)
		if s.Webs > 0 {
			fmt.Fprintf(sb, "（含 %d 个 Web 站点）", s.Webs)
		}
		sb.WriteString("。")
		if s.Probes > 0 {
			fmt.Fprintf(sb, "累计探测 %d 条路径", s.Probes)
			if model.fingers > 0 {
				fmt.Fprintf(sb, "、命中 %d 项 Web 指纹", model.fingers)
			}
			sb.WriteString("。")
		} else if model.fingers > 0 {
			fmt.Fprintf(sb, "命中 %d 项 Web 指纹。", model.fingers)
		}
		if s.Loots > 0 {
			fmt.Fprintf(sb, "**发现 %d 项需优先复核的安全发现（凭证 / 弱口令 / 漏洞）。**", s.Loots)
		}
		if s.Errors > 0 {
			fmt.Fprintf(sb, "另有 %d 处探测报错。", s.Errors)
		}
		if s.Duration != "" {
			fmt.Fprintf(sb, "全程耗时 %s。", s.Duration)
		}
		return
	}

	fmt.Fprintf(sb, "The scan identified %s across %s", reportPlural(model.hosts, "host", "hosts"), reportPlural(s.Services, "open service", "open services"))
	if s.Webs > 0 {
		fmt.Fprintf(sb, " (%s)", reportPlural(s.Webs, "web site", "web sites"))
	}
	sb.WriteString(". ")
	if s.Probes > 0 {
		fmt.Fprintf(sb, "It probed %s", reportPlural(s.Probes, "path", "paths"))
		if model.fingers > 0 {
			fmt.Fprintf(sb, " and matched %s", reportPlural(model.fingers, "fingerprint", "fingerprints"))
		}
		sb.WriteString(". ")
	} else if model.fingers > 0 {
		fmt.Fprintf(sb, "It matched %s. ", reportPlural(model.fingers, "fingerprint", "fingerprints"))
	}
	if s.Loots > 0 {
		fmt.Fprintf(sb, "**%s surfaced (credentials / weak passwords / vulnerabilities) — review these first.** ", reportPlural(s.Loots, "security finding", "security findings"))
	}
	if s.Errors > 0 {
		fmt.Fprintf(sb, "%s occurred during probing. ", reportPlural(s.Errors, "error", "errors"))
	}
	if s.Duration != "" {
		fmt.Fprintf(sb, "The scan took %s.", s.Duration)
	}
}

func writeMarkdownMetrics(sb *strings.Builder, t reportLang, model reportModel) {
	s := model.summary
	sb.WriteString("## " + t.tr("指标", "Metrics") + "\n\n")
	fmt.Fprintf(sb, "| %s | %s |\n", t.tr("指标", "Metric"), t.tr("数值", "Value"))
	sb.WriteString("| --- | ---: |\n")
	for _, row := range []struct {
		label string
		value any
	}{
		{t.tr("输入目标", "Inputs"), s.Targets},
		{t.tr("开放服务", "Open services"), s.Services},
		{t.tr("Web 站点", "Web endpoints"), s.Webs},
		{t.tr("路径探测", "Web probes"), s.Probes},
		{t.tr("Web 指纹", "Fingerprints"), model.fingers},
		{t.tr("安全发现", "Loots"), s.Loots},
		{t.tr("错误", "Errors"), s.Errors},
		{t.tr("任务", "Tasks"), s.Tasks},
		{t.tr("请求", "Requests"), s.Requests},
		{t.tr("耗时", "Duration"), s.Duration},
	} {
		fmt.Fprintf(sb, "| %s | %v |\n", row.label, row.value)
	}
	sb.WriteString("\n")
}

func writeMarkdownAsset(sb *strings.Builder, t reportLang, asset reportAsset, opts ReportOptions) {
	title := FirstNonEmpty(asset.title, t.tr("资产", "Asset"))
	if asset.target != "" && asset.target != title {
		fmt.Fprintf(sb, "### %s — `%s`\n\n", title, asset.target)
	} else {
		fmt.Fprintf(sb, "### %s\n\n", title)
	}

	writeMarkdownFact(sb, t, t.tr("开放服务", "Services"), asset.services)
	writeMarkdownFact(sb, t, t.tr("HTTP 响应", "HTTP"), asset.statuses)
	writeMarkdownFact(sb, t, t.tr("Web 指纹", "Fingerprints"), asset.fingers)
	if asset.paths > 0 {
		fmt.Fprintf(sb, "- %s%s%s\n", t.tr("已探测路径", "Paths"), t.sep(), t.tr(fmt.Sprintf("%d 条", asset.paths), strconv.Itoa(asset.paths)))
	}
	if asset.status != "" {
		fmt.Fprintf(sb, "- %s%s%s\n", t.tr("状态", "State"), t.sep(), markdownCode(asset.status))
	}
	sb.WriteString("\n")

	if opts.Sitemap && asset.sitemap != nil {
		sb.WriteString("#### " + t.tr("站点地图", "Sitemap") + "\n\n```text\n")
		renderSitemapNode(sb, asset.sitemap, "", true, NewColor(false))
		sb.WriteString("```\n\n")
	}
	writeMarkdownAnalysis(sb, t, asset.items)
}

func writeMarkdownFact(sb *strings.Builder, t reportLang, label string, values []string) {
	if len(values) == 0 {
		return
	}
	coded := make([]string, 0, len(values))
	for _, value := range values {
		coded = append(coded, markdownCode(value))
	}
	fmt.Fprintf(sb, "- %s%s%s\n", label, t.sep(), strings.Join(coded, t.tr("、", ", ")))
}

func writeMarkdownAnalysis(sb *strings.Builder, t reportLang, items []reportItem) {
	wrote := false
	for _, item := range items {
		switch item.kind {
		case AssetItemLoot, AssetItemNote, AssetItemResponse, AssetItemError:
		default:
			continue
		}
		if item.text == "" {
			continue
		}
		if !wrote {
			sb.WriteString("#### " + t.tr("分析研判", "Analysis") + "\n\n")
			wrote = true
		}
		fmt.Fprintf(sb, "##### %s\n\n", markdownHeading(item.text))
		switch {
		case item.detail != "" && strings.TrimSpace(item.text) != strings.TrimSpace(item.detail):
			sb.WriteString(item.detail)
			sb.WriteString("\n\n")
		case item.detail == "":
			sb.WriteString(item.text)
			sb.WriteString("\n\n")
		}
	}
}

// writeMarkdownInventory is the flat cross-asset listing the scan tool report
// has always carried: every service, probe, finding and error in one place.
func writeMarkdownInventory(sb *strings.Builder, t reportLang, model reportModel) {
	assets := make([]reportAsset, 0, len(model.assets)+len(model.bare))
	assets = append(assets, model.assets...)
	assets = append(assets, model.bare...)

	var services, paths, findings, errors []string
	for _, asset := range assets {
		for _, item := range asset.items {
			switch item.kind {
			case AssetItemService:
				services = append(services, fmt.Sprintf("- %s · %s\n",
					markdownCode(FirstNonEmpty(item.target, asset.label)), item.text))
			case AssetItemPath:
				paths = append(paths, "- "+strings.Join(pathInventoryParts(item), " · ")+"\n")
			case AssetItemLoot, AssetItemNote, AssetItemResponse:
				findings = append(findings, markdownStatusLine(findingInventoryLine(item), item.status))
			case AssetItemError:
				errors = append(errors, "- "+item.text+"\n")
			}
		}
	}

	writeMarkdownSection(sb, t.tr("开放服务", "Open Services"), services)
	writeMarkdownSection(sb, t.tr("Web 证据", "Web Evidence"), paths)
	writeMarkdownSection(sb, t.tr("安全发现", "Findings"), findings)
	writeMarkdownSection(sb, t.tr("错误", "Errors"), errors)
}

func pathInventoryParts(item reportItem) []string {
	parts := []string{markdownCode(FirstNonEmpty(item.target, item.text))}
	if item.status != "" {
		parts = append(parts, markdownCode(item.status))
	}
	if item.name != "" && !isStaticTitle(item.name) {
		parts = append(parts, strconv.Quote(item.name))
	}
	if len(item.fingers) > 0 {
		parts = append(parts, markdownCode(strings.Join(item.fingers, ",")))
	}
	return parts
}

func findingInventoryLine(item reportItem) string {
	line := item.text
	if item.target != "" {
		line += " — " + markdownCode(item.target)
	}
	return line
}

// markdownStatusLine carries the verification verdict into the bullet, so an
// unconfirmed finding cannot be mistaken for a proven one.
func markdownStatusLine(line, status string) string {
	if line == "" {
		return ""
	}
	switch status {
	case "not_confirmed":
		return "- ~~" + line + "~~ *(not confirmed)*\n"
	case "confirmed":
		return "- **[verified]** " + line + "\n"
	case "inconclusive":
		return "- **[inconclusive]** " + line + "\n"
	case "failed":
		return "- **[verification failed]** " + line + "\n"
	default:
		return "- " + line + "\n"
	}
}

func writeMarkdownSection(sb *strings.Builder, heading string, lines []string) {
	if len(lines) == 0 {
		return
	}
	sb.WriteString("## " + heading + "\n\n")
	for _, line := range lines {
		sb.WriteString(line)
	}
	sb.WriteString("\n")
}

func markdownCode(value string) string {
	value = strings.ReplaceAll(value, "`", "'")
	return "`" + value + "`"
}

func markdownHeading(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", " ")
	if value == "" {
		return "Analysis"
	}
	return strings.TrimLeft(value, "# ")
}

func reportPlural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// --- sitemap tree ---

type sitemapNode struct {
	segment     string
	status      string
	length      int
	title       string
	fingers     []string
	validated   bool
	isLeaf      bool
	annotations []string
	children    []*sitemapNode
}

// buildSitemapTree folds the asset's path items into a directory tree and hangs
// the fingerprint / finding annotations off the node they were found on.
// Returns nil when the asset has no paths, so callers can skip the section.
func buildSitemapTree(items []reportItem, annotations map[string][]string) *sitemapNode {
	paths := make([]reportItem, 0, len(items))
	for _, item := range items {
		if item.kind == AssetItemPath && item.text != "" {
			paths = append(paths, item)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].text < paths[j].text })

	root := &sitemapNode{segment: "/"}
	for _, item := range paths {
		node := root
		for _, part := range splitPath(item.text) {
			child := findSitemapChild(node, part)
			if child == nil {
				child = &sitemapNode{segment: part}
				node.children = append(node.children, child)
			}
			node = child
		}
		node.isLeaf = true
		node.status = item.status
		node.length = item.length
		node.title = item.name
		node.fingers = mergeStrings(node.fingers, item.fingers)
		node.validated = node.validated || item.validated
	}
	attachSitemapAnnotations(root, annotations)
	return root
}

func attachSitemapAnnotations(root *sitemapNode, annotations map[string][]string) {
	if values, ok := annotations["/"]; ok {
		root.annotations = append(root.annotations, values...)
	}
	for path, values := range annotations {
		if path == "/" {
			continue
		}
		node := root
		for _, part := range splitPath(path) {
			child := findSitemapChild(node, part)
			if child == nil {
				child = &sitemapNode{segment: part, isLeaf: true}
				node.children = append(node.children, child)
			}
			node = child
		}
		node.annotations = append(node.annotations, values...)
	}
}

func renderSitemapNode(sb *strings.Builder, node *sitemapNode, indent string, isRoot bool, c Color) {
	var line strings.Builder

	line.WriteString(indent)
	if !isRoot {
		line.WriteString("├── ")
	}

	if node.isLeaf && node.status != "" {
		line.WriteString(c.Status(fmt.Sprintf("[%-3s]", node.status)))
	} else {
		line.WriteString("     ")
	}
	line.WriteString(" ")

	path := "/" + node.segment
	if isRoot {
		path = "/"
	}
	switch {
	case node.validated:
		line.WriteString(c.GreenBold(path))
	case node.isLeaf:
		line.WriteString(path)
	default:
		line.WriteString(c.Dim(path))
	}

	if node.isLeaf && node.length > 0 {
		line.WriteString("  " + c.YellowBold(strconv.Itoa(node.length)))
	}
	if node.title != "" && !isStaticTitle(node.title) {
		line.WriteString("  " + c.Green(strconv.Quote(node.title)))
	}
	if len(node.fingers) > 0 {
		line.WriteString(" " + c.Cyan("["+strings.Join(node.fingers, ",")+"]"))
	}
	for _, annotation := range node.annotations {
		line.WriteString(" " + c.Yellow("{"+annotation+"}"))
	}

	sb.WriteString(line.String())
	sb.WriteByte('\n')

	for _, child := range node.children {
		childIndent := indent
		if !isRoot {
			childIndent += "│   "
		}
		renderSitemapNode(sb, child, childIndent, false, c)
	}
}

func findSitemapChild(node *sitemapNode, segment string) *sitemapNode {
	for _, child := range node.children {
		if child.segment == segment {
			return child
		}
	}
	return nil
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	if idx := strings.Index(parts[len(parts)-1], "?"); idx >= 0 {
		parts[len(parts)-1] = parts[len(parts)-1][:idx]
	}
	return parts
}

func pathFromTarget(target, assetTarget string) string {
	if target == "" {
		return ""
	}
	p := WebPath(target)
	if p == target && assetTarget != "" && strings.HasPrefix(target, assetTarget) {
		p = strings.TrimPrefix(target, assetTarget)
		if p == "" {
			p = "/"
		}
	}
	return p
}

func isStaticTitle(title string) bool {
	switch strings.ToLower(title) {
	case "js data", "css data", "ico data", "image data":
		return true
	}
	return false
}

func mergeStrings(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]struct{}, len(a))
	for _, s := range a {
		seen[strings.ToLower(s)] = struct{}{}
	}
	for _, s := range b {
		if _, ok := seen[strings.ToLower(s)]; !ok {
			a = append(a, s)
			seen[strings.ToLower(s)] = struct{}{}
		}
	}
	return a
}

func appendUniq(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}
