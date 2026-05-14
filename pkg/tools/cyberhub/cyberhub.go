package cyberhub

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/chainreactors/aiscan/pkg/resources"
	"github.com/chainreactors/aiscan/pkg/util"
	fingerslib "github.com/chainreactors/fingers/fingers"
	"github.com/chainreactors/neutron/templates"
	goflags "github.com/jessevdk/go-flags"
)

const (
	typeAll    = "all"
	typeFinger = "finger"
	typePOC    = "poc"
)

type Command struct {
	resources *resources.Set
}

type flags struct {
	Type      string   `short:"t" long:"type" description:"Resource type: finger, poc, or all"`
	Query     string   `short:"q" long:"query" description:"Search query"`
	Tags      []string `long:"tag" description:"Filter by tag. Can be comma-separated or repeated"`
	Protocol  string   `long:"protocol" description:"Filter fingerprints by protocol: http or tcp"`
	Fingers   []string `long:"finger" description:"Filter POCs by fingerprint name. Can be comma-separated or repeated"`
	Severity  []string `short:"s" long:"severity" description:"Filter POCs by severity. Can be comma-separated or repeated"`
	Limit     int      `long:"limit" description:"Maximum rows to print. Use 0 for all" default:"50"`
	JSONLines bool     `short:"j" long:"json" description:"Output JSON Lines"`
}

type item struct {
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
}

func New(resources *resources.Set) *Command {
	return &Command{resources: resources}
}

func (c *Command) Name() string { return "cyberhub" }

func (c *Command) Usage() string {
	return Usage()
}

func Usage() string {
	return `cyberhub - Search and list loaded fingerprints and POC templates
Usage:
  cyberhub list [finger|poc|all] [options]
  cyberhub search [finger|poc|all] <query> [options]

Options:
  -t, --type       Resource type: finger, poc, or all.
  -q, --query      Search query.
      --tag        Filter by tag. Can be comma-separated or repeated.
      --protocol   Filter fingerprints by protocol: http or tcp.
      --finger     Filter POCs by fingerprint name.
  -s, --severity   Filter POCs by severity.
      --limit      Maximum rows to print (default: 50, 0 for all).
  -j, --json       Output JSON Lines.

Examples:
  cyberhub list finger --limit 20
  cyberhub search finger nginx
  cyberhub list poc --severity critical,high
  cyberhub search poc spring --tag rce -j`
}

func (c *Command) Execute(_ context.Context, args []string) (string, error) {
	var opts flags
	parser := goflags.NewParser(&opts, goflags.Default&^goflags.PrintErrors)
	rest, err := parser.ParseArgs(args)
	if err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			return c.Usage() + "\n", nil
		}
		return "", fmt.Errorf("cyberhub: %w", err)
	}

	action, typ, query, err := parseAction(rest, opts.Type, opts.Query)
	if err != nil {
		return "", err
	}
	if opts.Limit < 0 {
		return "", fmt.Errorf("cyberhub: --limit cannot be negative")
	}

	items := c.collectItems(typ)
	items = filterItems(items, query, opts)
	sortItems(items)
	total := len(items)
	if opts.Limit > 0 && len(items) > opts.Limit {
		items = items[:opts.Limit]
	}
	return renderItems(items, total, action, typ, opts.JSONLines)
}

func parseAction(rest []string, flagType, flagQuery string) (string, string, string, error) {
	action := "list"
	if len(rest) > 0 {
		switch strings.ToLower(strings.TrimSpace(rest[0])) {
		case "list", "ls":
			action = "list"
			rest = rest[1:]
		case "search", "find":
			action = "search"
			rest = rest[1:]
		}
	}

	typ := normalizeType(flagType)
	if strings.TrimSpace(flagType) != "" && typ == "" {
		return "", "", "", fmt.Errorf("cyberhub: invalid type %q", flagType)
	}
	if typ == "" {
		typ = typeAll
	}
	if len(rest) > 0 {
		if candidate := normalizeType(rest[0]); candidate != "" {
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
	if action == "search" && query == "" {
		return "", "", "", fmt.Errorf("cyberhub: search requires a query")
	}
	return action, typ, query, nil
}

func normalizeType(value string) string {
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

func (c *Command) collectItems(typ string) []item {
	var out []item
	if typ == typeAll || typ == typeFinger {
		for _, finger := range c.fingers() {
			if finger == nil {
				continue
			}
			out = append(out, fingerItem(finger))
		}
	}
	if typ == typeAll || typ == typePOC {
		for _, tmpl := range c.templates() {
			if tmpl == nil {
				continue
			}
			out = append(out, templateItem(tmpl))
		}
	}
	return out
}

func (c *Command) fingers() fingerslib.Fingers {
	if c == nil || c.resources == nil || c.resources.FingersConfig == nil {
		return nil
	}
	return c.resources.FingersConfig.FullFingers.Fingers()
}

func (c *Command) templates() []*templates.Template {
	if c == nil || c.resources == nil || c.resources.NeutronConfig == nil {
		return nil
	}
	return c.resources.NeutronConfig.Templates.Templates()
}

func fingerItem(finger *fingerslib.Finger) item {
	protocol := strings.TrimSpace(finger.Protocol)
	if protocol == "" {
		protocol = "http"
	}
	out := item{
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

func templateItem(tmpl *templates.Template) item {
	name := tmpl.Info.Name
	if name == "" {
		name = tmpl.Id
	}
	return item{
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

func filterItems(items []item, query string, opts flags) []item {
	query = strings.ToLower(strings.TrimSpace(query))
	tags := normalizeValues(opts.Tags)
	fingers := normalizeValues(opts.Fingers)
	severities := normalizeValues(opts.Severity)
	protocol := strings.ToLower(strings.TrimSpace(opts.Protocol))

	out := make([]item, 0, len(items))
	for _, item := range items {
		if query != "" && !itemMatchesQuery(item, query) {
			continue
		}
		if protocol != "" && (item.Kind != typeFinger || !strings.EqualFold(item.Protocol, protocol)) {
			continue
		}
		if len(tags) > 0 && !intersectsNormalized(tags, item.Tags) {
			continue
		}
		if len(fingers) > 0 && (item.Kind != typePOC || !intersectsNormalized(fingers, item.Fingers)) {
			continue
		}
		if len(severities) > 0 && (item.Kind != typePOC || !containsNormalized(severities, item.Severity)) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func itemMatchesQuery(item item, query string) bool {
	values := []string{
		item.Kind,
		item.Name,
		item.ID,
		item.Protocol,
		item.Severity,
		item.Vendor,
		item.Product,
		item.Description,
		item.Author,
	}
	values = append(values, item.Tags...)
	values = append(values, item.Fingers...)
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func sortItems(items []item) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Name != right.Name {
			return strings.ToLower(left.Name) < strings.ToLower(right.Name)
		}
		return left.ID < right.ID
	})
}

func renderItems(items []item, total int, action, typ string, jsonLines bool) (string, error) {
	var sb strings.Builder
	if jsonLines {
		for _, item := range items {
			line, err := json.Marshal(item)
			if err != nil {
				return "", err
			}
			sb.Write(line)
			sb.WriteByte('\n')
		}
		return sb.String(), nil
	}

	for _, item := range items {
		sb.WriteString(formatItem(item))
		sb.WriteByte('\n')
	}
	sb.WriteString(fmt.Sprintf("[cyberhub] %s %s %d %d\n", action, typ, len(items), total))
	return sb.String(), nil
}

func formatItem(item item) string {
	parts := []string{"[cyberhub]", item.Kind, item.Name}
	switch item.Kind {
	case typeFinger:
		parts = util.AppendNonEmpty(parts, item.Protocol)
		if item.Focus {
			parts = append(parts, "focus")
		}
		if item.Active {
			parts = append(parts, "active")
		}
		if item.Level > 0 {
			parts = append(parts, strconv.Itoa(item.Level))
		}
		parts = util.AppendNonEmpty(parts, item.Vendor, item.Product)
	case typePOC:
		parts = util.AppendNonEmpty(parts, item.ID, item.Severity)
		if len(item.Fingers) > 0 {
			parts = append(parts, strings.Join(item.Fingers, ","))
		}
	}
	if len(item.Tags) > 0 {
		parts = append(parts, strings.Join(item.Tags, ","))
	}
	return strings.Join(util.QuoteFields(parts), " ")
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

func intersectsNormalized(want []string, got []string) bool {
	for _, value := range got {
		if containsNormalized(want, value) {
			return true
		}
	}
	return false
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
