package search

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/fingers/alias"
	fingerslib "github.com/chainreactors/fingers/fingers"
	"github.com/chainreactors/neutron/templates"
	"github.com/chainreactors/sdk/pkg/association"
	goflags "github.com/jessevdk/go-flags"
)

const (
	typeAll    = "all"
	typeFinger = "finger"
	typePOC    = "poc"
)

type CyberhubSearch struct {
	index *association.Index
}

type cyberhubFlags struct {
	Type      string   `short:"t" long:"type" description:"Resource type: finger, poc, or all"`
	Query     string   `short:"q" long:"query" description:"Search query"`
	Tags      []string `long:"tag" description:"Filter by tag. Can be comma-separated or repeated"`
	Protocol  string   `long:"protocol" description:"Filter fingerprints by protocol: http or tcp"`
	Fingers   []string `long:"finger" description:"Filter by fingerprint (association-aware)"`
	Severity  []string `short:"s" long:"severity" description:"Filter POCs by severity"`
	CVEs      []string `long:"cve" description:"Filter by CVE ID"`
	Vendor    string   `long:"vendor" description:"Filter by vendor name"`
	Product   string   `long:"product" description:"Filter by product name"`
	POC       bool     `long:"poc" description:"Only show entries with associated POC templates"`
	Limit     int      `long:"limit" description:"Maximum rows to print. Use 0 for all" default:"50"`
	JSONLines bool     `short:"j" long:"json" description:"Output JSON Lines"`
}

type cyberhubItem struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	ID          string   `json:"id,omitempty"`
	Protocol    string   `json:"protocol,omitempty"`
	Severity    string   `json:"severity,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Fingers     []string `json:"fingers,omitempty"`
	Focus       bool     `json:"focus,omitempty"`
	Active      bool     `json:"active,omitempty"`
	Level       int      `json:"level,omitempty"`
	Vendor      string   `json:"vendor,omitempty"`
	Product     string   `json:"product,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Associated  int      `json:"associated,omitempty"`
}

func NewCyberhubSearch(index *association.Index) *CyberhubSearch {
	return &CyberhubSearch{index: index}
}

func cyberhubUsage() string {
	return `search cyberhub - Search and list loaded fingerprints and POC templates
Usage:
  search cyberhub list [finger|poc|all] [options]
  search cyberhub search [finger|poc|all] <query> [options]
  search cyberhub id <name-or-id>

Options:
  -t, --type       Resource type: finger, poc, or all.
  -q, --query      Search query.
      --tag        Filter by tag. Can be comma-separated or repeated.
      --protocol   Filter fingerprints by protocol: http or tcp.
      --finger     Filter by fingerprint (association-aware: alias + CPE links).
  -s, --severity   Filter POCs by severity.
      --cve        Filter by CVE ID.
      --vendor     Filter by vendor name.
      --product    Filter by product name.
      --poc        Only show entries with associated POC templates.
      --limit      Maximum rows (default: 50, 0 for all).
  -j, --json       Output JSON Lines.

Examples:
  search cyberhub search --finger tomcat
  search cyberhub search --finger shiro --severity critical,high
  search cyberhub search --cve CVE-2021-44228
  search cyberhub search --vendor apache --product tomcat
  search cyberhub search finger --poc
  search cyberhub id tomcat
  search cyberhub list poc --severity critical --limit 10
  search cyberhub search poc seeyon`
}

func (c *CyberhubSearch) Name() string  { return "cyberhub" }
func (c *CyberhubSearch) Usage() string { return cyberhubUsage() }

func (c *CyberhubSearch) Execute(_ context.Context, args []string) error {
	if c.index == nil {
		return fmt.Errorf("search cyberhub: association index not available")
	}

	var opts cyberhubFlags
	parser := goflags.NewParser(&opts, goflags.Default&^goflags.PrintErrors)
	rest, err := parser.ParseArgs(args)
	if err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			fmt.Fprint(commands.Output, cyberhubUsage()+"\n")
			return nil
		}
		return fmt.Errorf("search cyberhub: %w", err)
	}
	if opts.Limit < 0 {
		return fmt.Errorf("search cyberhub: --limit cannot be negative")
	}

	action, typ, query, err := parseCyberhubAction(rest, opts.Type, opts.Query)
	if err != nil {
		return err
	}

	var out string
	if action == "id" {
		out, err = c.executeID(query, opts.JSONLines)
	} else {
		q := c.buildQuery(query, opts)
		result := c.index.Lookup(q)
		items := c.resultToItems(result, typ, opts)
		sortCyberhubItems(items)
		total := len(items)
		if opts.Limit > 0 && len(items) > opts.Limit {
			items = items[:opts.Limit]
		}
		out, err = renderCyberhubItems(items, total, action, typ, opts.JSONLines)
	}
	if err != nil {
		return err
	}
	if out != "" {
		fmt.Fprint(commands.Output, out)
	}
	return nil
}

// buildQuery constructs a single association.Query from all flags and text input.
// Empty query (no flags, no text) returns all entities via Lookup.
func (c *CyberhubSearch) buildQuery(text string, opts cyberhubFlags) *association.Query {
	q := association.NewQuery()
	if text != "" {
		q.WithSearch(text)
	}
	if len(opts.Fingers) > 0 {
		q.WithFingers(expandCSV(opts.Fingers)...)
	}
	if len(opts.CVEs) > 0 {
		q.WithCVEs(expandCSV(opts.CVEs)...)
	}
	if len(opts.Tags) > 0 {
		q.WithTags(expandCSV(opts.Tags)...)
	}
	if opts.Vendor != "" {
		q.WithAttr("vendor", opts.Vendor)
	}
	if opts.Product != "" {
		q.WithAttr("product", opts.Product)
	}
	return q
}

func (c *CyberhubSearch) resultToItems(result *association.QueryResult, typ string, opts cyberhubFlags) []cyberhubItem {
	if result == nil {
		return nil
	}

	severities := normalizeValues(opts.Severity)
	protocol := strings.ToLower(strings.TrimSpace(opts.Protocol))

	var items []cyberhubItem
	if typ == typeAll || typ == typeFinger {
		if opts.POC {
			for _, fc := range result.FingersWithTemplates(c.index) {
				item := fingerItem(fc.Finger)
				item.Associated = fc.TemplateCount
				if protocol != "" && !strings.EqualFold(item.Protocol, protocol) {
					continue
				}
				items = append(items, item)
			}
		} else {
			for _, f := range result.Fingers {
				if f == nil {
					continue
				}
				item := fingerItem(f)
				if protocol != "" && !strings.EqualFold(item.Protocol, protocol) {
					continue
				}
				items = append(items, item)
			}
		}
	}
	if typ == typeAll || typ == typePOC {
		for _, t := range result.Templates {
			if t == nil {
				continue
			}
			item := templateItem(t)
			if len(severities) > 0 && !containsNormalized(severities, item.Severity) {
				continue
			}
			items = append(items, item)
		}
	}
	return items
}

// executeID looks up a single entity by name/id and shows detail + associations.
func (c *CyberhubSearch) executeID(name string, jsonOutput bool) (string, error) {
	if name == "" {
		return "", fmt.Errorf("search cyberhub id: name or id required")
	}

	if f := c.index.Finger(name); f != nil {
		return c.renderFingerDetail(f, jsonOutput)
	}
	if t := c.index.Template(name); t != nil {
		return c.renderTemplateDetail(t, jsonOutput)
	}
	if a := c.index.Alias(name); a != nil {
		return c.renderAliasDetail(a, jsonOutput)
	}
	return "", fmt.Errorf("search cyberhub id: %q not found", name)
}

type detailResult struct {
	Item       cyberhubItem   `json:"item"`
	Associated []cyberhubItem `json:"associated,omitempty"`
}

func (c *CyberhubSearch) renderFingerDetail(f *fingerslib.Finger, jsonOutput bool) (string, error) {
	item := fingerItem(f)
	result := c.index.Lookup(association.NewQuery().WithFingers(f.Name))

	var associated []cyberhubItem
	if result != nil {
		for _, t := range result.Templates {
			if t != nil {
				associated = append(associated, templateItem(t))
			}
		}
	}

	if jsonOutput {
		data, _ := json.Marshal(detailResult{Item: item, Associated: associated})
		return string(data) + "\n", nil
	}

	var sb strings.Builder
	sb.WriteString(formatCyberhubItem(item))
	sb.WriteByte('\n')
	if len(associated) > 0 {
		sb.WriteString(fmt.Sprintf("  associated POCs (%d):\n", len(associated)))
		for _, a := range associated {
			sb.WriteString(fmt.Sprintf("    %-30s %-10s %s\n", a.ID, a.Severity, a.Name))
		}
	} else {
		sb.WriteString("  no associated POCs\n")
	}
	return sb.String(), nil
}

func (c *CyberhubSearch) renderTemplateDetail(t *templates.Template, jsonOutput bool) (string, error) {
	item := templateItem(t)
	result := c.index.Lookup(association.NewQuery().WithTemplates(t.Id))

	var associated []cyberhubItem
	if result != nil {
		for _, f := range result.Fingers {
			if f != nil {
				associated = append(associated, fingerItem(f))
			}
		}
	}

	if jsonOutput {
		data, _ := json.Marshal(detailResult{Item: item, Associated: associated})
		return string(data) + "\n", nil
	}

	var sb strings.Builder
	sb.WriteString(formatCyberhubItem(item))
	sb.WriteByte('\n')
	if t.Info.Description != "" {
		sb.WriteString(fmt.Sprintf("  description: %s\n", t.Info.Description))
	}
	if len(associated) > 0 {
		sb.WriteString(fmt.Sprintf("  associated fingerprints (%d):\n", len(associated)))
		for _, a := range associated {
			sb.WriteString(fmt.Sprintf("    %s\n", a.Name))
		}
	}
	return sb.String(), nil
}

func (c *CyberhubSearch) renderAliasDetail(a *alias.Alias, jsonOutput bool) (string, error) {
	result := c.index.Lookup(association.NewQuery().WithAliases(a.Name))
	var items []cyberhubItem
	if result != nil {
		for _, f := range result.Fingers {
			if f != nil {
				items = append(items, fingerItem(f))
			}
		}
		for _, t := range result.Templates {
			if t != nil {
				items = append(items, templateItem(t))
			}
		}
	}

	if jsonOutput {
		data, _ := json.Marshal(map[string]interface{}{
			"alias":      a.Name,
			"vendor":     a.Vendor,
			"product":    a.Product,
			"associated": items,
		})
		return string(data) + "\n", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[alias] %s", a.Name))
	if a.Vendor != "" {
		sb.WriteString(fmt.Sprintf("  vendor=%s", a.Vendor))
	}
	if a.Product != "" {
		sb.WriteString(fmt.Sprintf("  product=%s", a.Product))
	}
	sb.WriteByte('\n')
	if len(items) > 0 {
		sb.WriteString(fmt.Sprintf("  associated (%d):\n", len(items)))
		for _, item := range items {
			sb.WriteString(fmt.Sprintf("    %s %s\n", item.Kind, item.Name))
		}
	}
	return sb.String(), nil
}

// --- action parsing ---

func parseCyberhubAction(rest []string, flagType, flagQuery string) (string, string, string, error) {
	action := "list"
	if len(rest) > 0 {
		switch strings.ToLower(strings.TrimSpace(rest[0])) {
		case "list", "ls":
			action = "list"
			rest = rest[1:]
		case "search", "find":
			action = "search"
			rest = rest[1:]
		case "id", "info", "show":
			action = "id"
			rest = rest[1:]
			name := strings.TrimSpace(strings.Join(rest, " "))
			if name == "" {
				return "", "", "", fmt.Errorf("search cyberhub id: name or id required")
			}
			return action, "", name, nil
		}
	}

	typ := normalizeCyberhubType(flagType)
	if strings.TrimSpace(flagType) != "" && typ == "" {
		return "", "", "", fmt.Errorf("search cyberhub: invalid type %q", flagType)
	}
	if typ == "" {
		typ = typeAll
	}
	if len(rest) > 0 {
		if candidate := normalizeCyberhubType(rest[0]); candidate != "" {
			typ = candidate
			rest = rest[1:]
		}
	}
	query := strings.TrimSpace(flagQuery)
	if query == "" && len(rest) > 0 {
		query = strings.TrimSpace(strings.Join(rest, " "))
		if query != "" {
			action = "search"
		}
	}
	return action, typ, query, nil
}

func normalizeCyberhubType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case "all", "*":
		return typeAll
	case "finger", "fingers", "fingerprint", "fingerprints", "fp":
		return typeFinger
	case "poc", "pocs", "template", "templates", "neutron":
		return typePOC
	default:
		return ""
	}
}

// --- item construction ---

func fingerItem(finger *fingerslib.Finger) cyberhubItem {
	protocol := strings.TrimSpace(finger.Protocol)
	if protocol == "" {
		protocol = "http"
	}
	out := cyberhubItem{
		Kind:        typeFinger,
		Name:        finger.Name,
		Protocol:    protocol,
		Tags:        append([]string(nil), finger.Tags...),
		Focus:       finger.Focus,
		Active:      finger.IsActive || finger.SendDataStr != "",
		Level:       finger.Level,
		Description: finger.Description,
		Author:      finger.Author,
	}
	if finger.Attributes.Vendor != "" {
		out.Vendor = finger.Attributes.Vendor
	}
	if finger.Attributes.Product != "" {
		out.Product = finger.Attributes.Product
	}
	return out
}

func templateItem(tmpl *templates.Template) cyberhubItem {
	name := tmpl.Info.Name
	if name == "" {
		name = tmpl.Id
	}
	return cyberhubItem{
		Kind:        typePOC,
		Name:        name,
		ID:          tmpl.Id,
		Severity:    strings.ToLower(strings.TrimSpace(tmpl.Info.Severity)),
		Tags:        splitList(tmpl.Info.Tags),
		Fingers:     append([]string(nil), tmpl.Fingers...),
		Description: tmpl.Info.Description,
		Author:      tmpl.Info.Author,
	}
}

// --- rendering ---

func sortCyberhubItems(items []cyberhubItem) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	})
}

func renderCyberhubItems(items []cyberhubItem, total int, action, typ string, jsonLines bool) (string, error) {
	var sb strings.Builder
	if jsonLines {
		for _, item := range items {
			line, _ := json.Marshal(item)
			sb.Write(line)
			sb.WriteByte('\n')
		}
		return sb.String(), nil
	}
	for _, item := range items {
		sb.WriteString(formatCyberhubItem(item))
		sb.WriteByte('\n')
	}
	sb.WriteString(fmt.Sprintf("[cyberhub] %s %s %d %d\n", action, typ, len(items), total))
	return sb.String(), nil
}

func formatCyberhubItem(item cyberhubItem) string {
	parts := []string{"[cyberhub]", item.Kind, item.Name}
	switch item.Kind {
	case typeFinger:
		parts = appendNonEmpty(parts, item.Protocol)
		if item.Focus {
			parts = append(parts, "focus")
		}
		if item.Active {
			parts = append(parts, "active")
		}
		if item.Level > 0 {
			parts = append(parts, strconv.Itoa(item.Level))
		}
		parts = appendNonEmpty(parts, item.Vendor, item.Product)
		if item.Associated > 0 {
			parts = append(parts, fmt.Sprintf("pocs=%d", item.Associated))
		}
	case typePOC:
		parts = appendNonEmpty(parts, item.ID, item.Severity)
		if len(item.Fingers) > 0 {
			parts = append(parts, strings.Join(item.Fingers, ","))
		}
	}
	if len(item.Tags) > 0 {
		parts = append(parts, strings.Join(item.Tags, ","))
	}
	return strings.Join(quoteFields(parts), " ")
}

// --- helpers ---

func expandCSV(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizeValues(values []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, value := range values {
		for _, part := range splitList(value) {
			part = strings.ToLower(part)
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			out = append(out, part)
		}
	}
	return out
}

func containsNormalized(want []string, got string) bool {
	got = strings.ToLower(strings.TrimSpace(got))
	for _, value := range want {
		if value == got {
			return true
		}
	}
	return false
}

func appendNonEmpty(parts []string, values ...string) []string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			parts = append(parts, v)
		}
	}
	return parts
}

func needsQuoting(value string) bool {
	return strings.ContainsAny(value, " \t\r\n\"")
}

func formatValue(value string) string {
	value = strings.TrimSpace(value)
	if needsQuoting(value) {
		return strconv.Quote(value)
	}
	return value
}

func quoteFields(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, formatValue(part))
	}
	return out
}
