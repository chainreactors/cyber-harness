package record

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	reset  = "\x1b[0m"
	dim    = "\x1b[2m"
	bold   = "\x1b[1m"
	cyan   = "\x1b[36m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	red    = "\x1b[31m"
	blue   = "\x1b[34m"
	magenta = "\x1b[35m"
)

func RenderFile(path, format, output string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var records []Record
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		r, err := Parse(scanner.Bytes())
		if err != nil {
			continue
		}
		records = append(records, r)
	}

	var w io.Writer = os.Stdout
	if output != "" {
		outFile, err := os.Create(output)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer outFile.Close()
		w = outFile
	}

	switch strings.ToLower(format) {
	case "markdown", "md":
		return renderMarkdown(w, records)
	default:
		return renderTerminal(w, records)
	}
}

func renderTerminal(w io.Writer, records []Record) error {
	for _, r := range records {
		switch r.Type {
		case TypeScanStart:
			d, _ := ParseData[ScanStart](r)
			fmt.Fprintf(w, "%s[scan]%s %s%s%s targets=%s mode=%s\n",
				bold+cyan, reset, dim, r.Timestamp.Format("15:04:05"), reset,
				strings.Join(d.Targets, ","), d.Mode)

		case TypeService:
			d, _ := ParseData[Service](r)
			fmt.Fprintf(w, "%s[service]%s %s %s %s\n",
				green, reset, d.Target, d.Protocol, d.Banner)

		case TypeWeb:
			d, _ := ParseData[Web](r)
			fingers := ""
			if len(d.Fingers) > 0 {
				fingers = " [" + strings.Join(d.Fingers, ",") + "]"
			}
			fmt.Fprintf(w, "%s[web]%s %s %d %s%s\n",
				green, reset, d.URL, d.Status, d.Title, fingers)

		case TypeFinding:
			d, _ := ParseData[Finding](r)
			color := yellow
			if d.Priority == "high" || d.Priority == "critical" {
				color = red
			}
			fmt.Fprintf(w, "%s[%s]%s %s %s\n",
				color, d.Kind, reset, d.Target, d.Summary)

		case TypeAISkill:
			d, _ := ParseData[AISkill](r)
			color := green
			if d.Status == "inconclusive" {
				color = yellow
			}
			fmt.Fprintf(w, "%s[ai:%s]%s %s %s — %s %s(%.1fs)%s\n",
				color, d.Skill, reset, d.Target, d.Status, d.Summary, dim, d.Duration, reset)

		case TypeAITurn:
			d, _ := ParseData[AITurn](r)
			fmt.Fprintf(w, "  %s[turn %d]%s %s→%s %s\n",
				magenta, d.Turn, reset, dim, reset, truncateStr(d.Request.Content, 80))
			fmt.Fprintf(w, "  %s[turn %d]%s %s←%s %s\n",
				blue, d.Turn, reset, dim, reset, truncateStr(d.Response.Content, 120))
			for _, tc := range d.ToolCalls {
				status := green + "ok" + reset
				if tc.IsError {
					status = red + "err" + reset
				}
				fmt.Fprintf(w, "    %s⚡%s %s(%s) [%s]\n",
					yellow, reset, tc.Name, truncateStr(tc.Arguments, 60), status)
			}

		case TypeScanEnd:
			d, _ := ParseData[ScanEnd](r)
			fmt.Fprintf(w, "%s[done]%s %.1fs targets=%d services=%d webs=%d findings=%d ai=%d errors=%d\n",
				dim, reset, d.Duration, d.Targets, d.Services, d.Webs, d.Findings, d.AISkills, d.Errors)
		}
	}
	return nil
}

func renderMarkdown(w io.Writer, records []Record) error {
	fmt.Fprintln(w, "# Scan Record")
	fmt.Fprintln(w)

	// Group by type
	var services []Service
	var webs []Web
	var findings []Finding
	var aiSkills []AISkill
	var aiTurns []AITurn
	var scanEnd *ScanEnd

	for _, r := range records {
		switch r.Type {
		case TypeService:
			d, _ := ParseData[Service](r)
			services = append(services, d)
		case TypeWeb:
			d, _ := ParseData[Web](r)
			webs = append(webs, d)
		case TypeFinding:
			d, _ := ParseData[Finding](r)
			findings = append(findings, d)
		case TypeAISkill:
			d, _ := ParseData[AISkill](r)
			aiSkills = append(aiSkills, d)
		case TypeAITurn:
			d, _ := ParseData[AITurn](r)
			aiTurns = append(aiTurns, d)
		case TypeScanEnd:
			d, _ := ParseData[ScanEnd](r)
			scanEnd = &d
		}
	}

	if scanEnd != nil {
		fmt.Fprintf(w, "## Summary\n\n")
		fmt.Fprintf(w, "| Metric | Value |\n|---|---:|\n")
		fmt.Fprintf(w, "| Duration | %.1fs |\n", scanEnd.Duration)
		fmt.Fprintf(w, "| Services | %d |\n", scanEnd.Services)
		fmt.Fprintf(w, "| Web | %d |\n", scanEnd.Webs)
		fmt.Fprintf(w, "| Findings | %d |\n", scanEnd.Findings)
		fmt.Fprintf(w, "| AI Skills | %d |\n", scanEnd.AISkills)
		fmt.Fprintln(w)
	}

	if len(services) > 0 {
		fmt.Fprintf(w, "## Services\n\n")
		for _, d := range services {
			fmt.Fprintf(w, "- `%s:%d` %s\n", d.Target, d.Port, d.Protocol)
		}
		fmt.Fprintln(w)
	}

	if len(webs) > 0 {
		fmt.Fprintf(w, "## Web Endpoints\n\n")
		for _, d := range webs {
			fmt.Fprintf(w, "- `%s` %d %s\n", d.URL, d.Status, d.Title)
		}
		fmt.Fprintln(w)
	}

	if len(findings) > 0 {
		fmt.Fprintf(w, "## Findings\n\n")
		for _, d := range findings {
			fmt.Fprintf(w, "- **[%s]** `%s` %s — %s\n", d.Priority, d.Target, d.Kind, d.Summary)
		}
		fmt.Fprintln(w)
	}

	if len(aiSkills) > 0 {
		fmt.Fprintf(w, "## AI Skill Results\n\n")
		for _, d := range aiSkills {
			fmt.Fprintf(w, "### %s → %s (%.1fs)\n\n", d.Skill, d.Target, d.Duration)
			fmt.Fprintf(w, "**Status:** %s\n\n", d.Status)
			fmt.Fprintf(w, "%s\n\n", d.Summary)
			if d.Detail != "" {
				fmt.Fprintf(w, "> %s\n\n", d.Detail)
			}
		}
	}

	if len(aiTurns) > 0 {
		fmt.Fprintf(w, "## AI Execution Trace\n\n")
		for _, d := range aiTurns {
			fmt.Fprintf(w, "#### [%s] Turn %d (%.1fs)\n\n", d.Skill, d.Turn, d.Duration)
			fmt.Fprintf(w, "**Request:** %s\n\n", truncateStr(d.Request.Content, 200))
			fmt.Fprintf(w, "**Response:** %s\n\n", truncateStr(d.Response.Content, 300))
			if len(d.ToolCalls) > 0 {
				fmt.Fprintf(w, "**Tools:**\n")
				for _, tc := range d.ToolCalls {
					fmt.Fprintf(w, "- `%s` %s\n", tc.Name, truncateStr(tc.Arguments, 100))
				}
				fmt.Fprintln(w)
			}
		}
	}

	return nil
}

func truncateStr(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
