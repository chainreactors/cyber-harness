package web

import "github.com/chainreactors/aiscan/core/output"

// defaultReportLang is the language the report is frozen in at scan time; the
// stored copy is only a fallback because GetReport re-renders per request.
const defaultReportLang = "zh"

func buildMarkdownReport(target, mode string, result *output.Result, lang string) string {
	return output.RenderReport(result, output.ReportOptions{
		Style:        output.StyleMarkdown,
		Lang:         lang,
		Title:        target,
		Mode:         mode,
		Sitemap:      true,
		CollapseBare: true,
	})
}
