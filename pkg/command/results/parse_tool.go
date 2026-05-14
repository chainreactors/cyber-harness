package results

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chainreactors/parsers"
	"github.com/chainreactors/utils"
	zombiepkg "github.com/chainreactors/zombie/pkg"
)

func parseGogoResults(lines []string, analysis string) (string, error) {
	var results []*parsers.GOGOResult
	for _, line := range lines {
		var r parsers.GOGOResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		results = append(results, &r)
	}
	if len(results) == 0 {
		return "No valid gogo results parsed.", nil
	}

	var sb strings.Builder

	if analysis == "summary" || analysis == "all" {
		sb.WriteString(fmt.Sprintf("## Summary\nTotal results: %d\n", len(results)))
		ips := uniqueValues(results, "ip")
		ports := uniqueValues(results, "port")
		sb.WriteString(fmt.Sprintf("Unique IPs: %d\nUnique ports: %d\n", len(ips), len(ports)))

		httpCount := 0
		frameworkCount := 0
		vulnCount := 0
		for _, r := range results {
			if r.IsHttp() {
				httpCount++
			}
			if len(parsers.FrameworkNames(r.Frameworks)) > 0 {
				frameworkCount++
			}
			if len(r.Vulns) > 0 {
				vulnCount++
			}
		}
		sb.WriteString(fmt.Sprintf("HTTP services: %d\nWith fingerprints: %d\nWith vulns: %d\n", httpCount, frameworkCount, vulnCount))
	}

	if analysis == "targets" || analysis == "all" {
		sb.WriteString("\n## Derived Targets\n")

		var webURLs, zombieTargets, pocTargets []string
		for _, r := range results {
			if r.IsHttp() {
				webURLs = append(webURLs, r.GetBaseURL())
			}
			if service := gogoZombieService(r); service != "" {
				zombieTargets = append(zombieTargets, fmt.Sprintf("%s://%s:%s", service, r.Ip, r.Port))
			}
			fingers := parsers.FrameworkNames(r.Frameworks)
			if len(fingers) > 0 || r.IsHttp() {
				pocTargets = append(pocTargets, r.GetTarget())
			}
		}

		sb.WriteString(fmt.Sprintf("\nWeb URLs (%d):\n", len(webURLs)))
		for _, u := range webURLs {
			sb.WriteString("  - " + u + "\n")
		}
		sb.WriteString(fmt.Sprintf("\nZombie targets (%d):\n", len(zombieTargets)))
		for _, t := range zombieTargets {
			sb.WriteString("  - " + t + "\n")
		}
		sb.WriteString(fmt.Sprintf("\nPOC targets (%d):\n", len(pocTargets)))
		for _, t := range pocTargets {
			sb.WriteString("  - " + t + "\n")
		}
	}

	if analysis == "stats" || analysis == "all" {
		sb.WriteString("\n## Statistics\n")
		writeDistribution(&sb, "Port", countValues(results, "port"))
		writeDistribution(&sb, "Protocol", countValues(results, "protocol"))
		writeDistribution(&sb, "Status", countValues(results, "status"))

		frameCounts := make(map[string]int)
		for _, r := range results {
			for _, name := range parsers.FrameworkNames(r.Frameworks) {
				frameCounts[name]++
			}
		}
		if len(frameCounts) > 0 {
			writeDistributionMap(&sb, "Frameworks", frameCounts)
		}
	}

	return truncateOutput(sb.String()), nil
}

func parseSprayResults(lines []string, analysis string) (string, error) {
	var results []*parsers.SprayResult
	for _, line := range lines {
		var r parsers.SprayResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		if r.ErrString != "" {
			continue
		}
		results = append(results, &r)
	}
	if len(results) == 0 {
		return "No valid spray results parsed.", nil
	}

	var sb strings.Builder

	if analysis == "summary" || analysis == "all" {
		sb.WriteString(fmt.Sprintf("## Summary\nTotal results: %d\n", len(results)))
		urls := make(map[string]struct{})
		hosts := make(map[string]struct{})
		for _, r := range results {
			urls[r.UrlString] = struct{}{}
			if r.Host != "" {
				hosts[r.Host] = struct{}{}
			}
		}
		sb.WriteString(fmt.Sprintf("Unique URLs: %d\nUnique hosts: %d\n", len(urls), len(hosts)))

		frameworkCount := 0
		for _, r := range results {
			if len(parsers.FrameworkNames(r.Frameworks)) > 0 {
				frameworkCount++
			}
		}
		sb.WriteString(fmt.Sprintf("With fingerprints: %d\n", frameworkCount))
	}

	if analysis == "targets" || analysis == "all" {
		sb.WriteString("\n## Derived Targets\n")
		var pocTargets []string
		for _, r := range results {
			fingers := parsers.FrameworkNames(r.Frameworks)
			if len(fingers) > 0 && r.Status > 0 {
				pocTargets = append(pocTargets, r.UrlString)
			}
		}
		sb.WriteString(fmt.Sprintf("\nPOC targets (%d):\n", len(pocTargets)))
		for _, t := range pocTargets {
			sb.WriteString("  - " + t + "\n")
		}
	}

	if analysis == "stats" || analysis == "all" {
		sb.WriteString("\n## Statistics\n")

		statusCounts := make(map[string]int)
		for _, r := range results {
			statusCounts[fmt.Sprintf("%d", r.Status)]++
		}
		writeDistributionMap(&sb, "Status", statusCounts)

		sourceCounts := make(map[string]int)
		for _, r := range results {
			sourceCounts[r.Source.Name()]++
		}
		writeDistributionMap(&sb, "Source", sourceCounts)

		frameCounts := make(map[string]int)
		for _, r := range results {
			for _, name := range parsers.FrameworkNames(r.Frameworks) {
				frameCounts[name]++
			}
		}
		if len(frameCounts) > 0 {
			writeDistributionMap(&sb, "Frameworks", frameCounts)
		}
	}

	return truncateOutput(sb.String()), nil
}

func parseZombieResults(lines []string, analysis string) (string, error) {
	var results []*parsers.ZombieResult
	for _, line := range lines {
		var r parsers.ZombieResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		results = append(results, &r)
	}
	if len(results) == 0 {
		return "No valid zombie results parsed.", nil
	}

	var sb strings.Builder

	if analysis == "summary" || analysis == "all" {
		sb.WriteString(fmt.Sprintf("## Summary\nTotal findings: %d\n", len(results)))
		services := make(map[string]struct{})
		hosts := make(map[string]struct{})
		for _, r := range results {
			services[r.Service] = struct{}{}
			hosts[r.Address()] = struct{}{}
		}
		sb.WriteString(fmt.Sprintf("Unique services: %d\nUnique hosts: %d\n", len(services), len(hosts)))
	}

	if analysis == "targets" || analysis == "all" {
		sb.WriteString("\n## Credentials Found\n")
		for _, r := range results {
			sb.WriteString(fmt.Sprintf("  - %s %s:%s (mod=%s)\n", r.URI(), r.Username, r.Password, r.Mod.String()))
		}
	}

	if analysis == "stats" || analysis == "all" {
		sb.WriteString("\n## Statistics\n")
		serviceCounts := make(map[string]int)
		modCounts := make(map[string]int)
		for _, r := range results {
			serviceCounts[r.Service]++
			modCounts[r.Mod.String()]++
		}
		writeDistributionMap(&sb, "Service", serviceCounts)
		writeDistributionMap(&sb, "Mod", modCounts)
	}

	return truncateOutput(sb.String()), nil
}

func gogoZombieService(result *parsers.GOGOResult) string {
	if result == nil {
		return ""
	}
	for _, name := range parsers.FrameworkNames(result.Frameworks) {
		if service, ok := parsers.ZombieServiceFromName(name); ok {
			return service
		}
	}
	if utils.IsWebPort(result.Port) {
		return ""
	}
	service := zombiepkg.GetDefault(result.Port)
	if service == "" || service == "unknown" {
		return ""
	}
	return service
}

func uniqueValues(results []*parsers.GOGOResult, key string) map[string]struct{} {
	m := make(map[string]struct{})
	for _, r := range results {
		if v := r.Get(key); v != "" {
			m[v] = struct{}{}
		}
	}
	return m
}

func countValues(results []*parsers.GOGOResult, key string) map[string]int {
	m := make(map[string]int)
	for _, r := range results {
		if v := r.Get(key); v != "" {
			m[v]++
		}
	}
	return m
}

func writeDistribution(sb *strings.Builder, label string, counts map[string]int) {
	writeDistributionMap(sb, label, counts)
}

func writeDistributionMap(sb *strings.Builder, label string, counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	type kv struct {
		Key   string
		Count int
	}
	sorted := make([]kv, 0, len(counts))
	for k, v := range counts {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Count > sorted[j].Count })

	sb.WriteString(fmt.Sprintf("\n%s distribution:\n", label))
	limit := 20
	for i, item := range sorted {
		if i >= limit {
			sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(sorted)-limit))
			break
		}
		sb.WriteString(fmt.Sprintf("  %-20s %d\n", item.Key, item.Count))
	}
}

func splitJSONLines(data string) []string {
	var lines []string
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line[0] == '{' {
			lines = append(lines, line)
		}
	}
	return lines
}

const maxToolOutput = 50 * 1024

func truncateOutput(s string) string {
	if len(s) <= maxToolOutput {
		return s
	}
	return s[:maxToolOutput] + "\n... (output truncated)"
}
