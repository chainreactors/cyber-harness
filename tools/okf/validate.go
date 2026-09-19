// Package okf validates Open Knowledge Format bundles.
package okf

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

const Version = "0.2"

type Level string

const (
	Error   Level = "error"
	Warning Level = "warning"
)

type Issue struct {
	Level   Level  `json:"level"`
	Path    string `json:"path"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type Report struct {
	Version string  `json:"version"`
	Strict  bool    `json:"strict"`
	Issues  []Issue `json:"issues"`
}

func (r Report) Valid() bool {
	for _, issue := range r.Issues {
		if issue.Level == Error {
			return false
		}
	}
	return true
}

type document struct {
	abs      string
	rel      string
	root     string
	reserved bool
	body     []byte
	meta     map[string]any
	links    []string
}

var logDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func Validate(ctx context.Context, target string, strict bool) (Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return Report{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Report{}, err
	}
	root := abs
	if !info.IsDir() {
		root = filepath.Dir(abs)
	}
	report := Report{Version: Version, Strict: strict}
	var paths []string
	if info.IsDir() {
		err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				paths = append(paths, path)
			}
			return ctx.Err()
		})
	} else if strings.EqualFold(filepath.Ext(abs), ".md") {
		paths = []string{abs}
	} else {
		return Report{}, fmt.Errorf("OKF target must be a markdown file or directory")
	}
	if err != nil {
		return Report{}, err
	}
	sort.Strings(paths)
	documents := make(map[string]*document, len(paths))
	for _, path := range paths {
		doc := inspect(path, root, strict, &report)
		if doc != nil {
			documents[filepath.ToSlash(doc.rel)] = doc
		}
	}
	checkGraph(root, documents, strict, &report)
	return report, nil
}

func inspect(path, root string, strict bool, report *Report) *document {
	raw, err := os.ReadFile(path)
	rel, _ := filepath.Rel(root, path)
	rel = filepath.ToSlash(rel)
	if err != nil {
		add(report, Error, rel, "read", err.Error())
		return nil
	}
	if !utf8.Valid(raw) {
		add(report, Error, rel, "utf8", "markdown must be valid UTF-8")
		return nil
	}
	name := strings.ToLower(filepath.Base(path))
	reserved := name == "index.md" || name == "log.md"
	frontmatter, body, hasFrontmatter, err := splitFrontmatter(raw)
	if err != nil {
		add(report, Error, rel, "frontmatter", err.Error())
		return nil
	}
	meta := map[string]any{}
	if hasFrontmatter {
		if err := yaml.Unmarshal(frontmatter, &meta); err != nil {
			add(report, Error, rel, "frontmatter.yaml", err.Error())
			return nil
		}
	}
	if reserved {
		if hasFrontmatter && !(name == "index.md" && filepath.Clean(path) == filepath.Join(root, "index.md")) {
			add(report, Error, rel, "reserved.frontmatter", "only the bundle-root index.md may contain frontmatter")
		}
		if name == "index.md" && hasFrontmatter {
			version, _ := meta["okf_version"].(string)
			if version == "" {
				add(report, Error, rel, "version", "root index frontmatter may only declare a non-empty okf_version")
			} else if version != Version {
				level := Warning
				if strict {
					level = Error
				}
				add(report, level, rel, "version", fmt.Sprintf("unsupported OKF version %q; validating as %s", version, Version))
			}
			if len(meta) != 1 {
				add(report, Error, rel, "version", "root index frontmatter may contain only okf_version")
			}
		}
		checkReserved(name, rel, body, report)
	} else {
		if !hasFrontmatter {
			add(report, Error, rel, "concept.frontmatter", "concept document requires YAML frontmatter")
		} else {
			checkConcept(rel, meta, body, strict, report)
		}
	}
	return &document{abs: path, rel: rel, root: root, reserved: reserved, body: body, meta: meta, links: markdownLinks(body)}
}

func splitFrontmatter(raw []byte) (frontmatter, body []byte, found bool, err error) {
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		return nil, raw, false, nil
	}
	end := bytes.Index(raw[4:], []byte("\n---\n"))
	if end >= 0 {
		return raw[4 : 4+end], raw[4+end+5:], true, nil
	}
	if bytes.HasSuffix(raw, []byte("\n---")) {
		return raw[4 : len(raw)-4], nil, true, nil
	}
	return nil, nil, true, fmt.Errorf("frontmatter closing delimiter is missing")
}

func checkConcept(path string, meta map[string]any, body []byte, strict bool, report *Report) {
	typeName, _ := meta["type"].(string)
	if strings.TrimSpace(typeName) == "" {
		add(report, Error, path, "concept.type", "frontmatter requires a non-empty type")
	}
	recommended := strictLevel(strict)
	for _, key := range []string{"title", "description"} {
		if value, _ := meta[key].(string); strings.TrimSpace(value) == "" {
			add(report, recommended, path, "concept."+key, key+" is recommended")
		}
	}
	if value, exists := meta["status"]; exists {
		status, ok := value.(string)
		if !ok || (status != "draft" && status != "stable" && status != "deprecated") {
			add(report, recommended, path, "lifecycle.status", "status should be draft, stable, or deprecated")
		}
	}
	if value, exists := meta["stale_after"]; exists {
		checkTime(path, "stale_after", value, recommended, report)
	}
	if value, exists := meta["generated"]; exists {
		if generated, ok := mapping(value); ok {
			checkActorEvent(path, "generated", generated, recommended, report)
		} else {
			add(report, recommended, path, "generated", "generated should be a mapping")
		}
	}
	if value, exists := meta["verified"]; exists {
		verified := mappings(value)
		if len(verified) == 0 {
			add(report, recommended, path, "verified", "verified should be a mapping or list of mappings")
		}
		for index, event := range verified {
			checkActorEvent(path, fmt.Sprintf("verified[%d]", index), event, recommended, report)
		}
	}
	if value, exists := meta["sources"]; exists {
		sources := mappings(value)
		if len(sources) == 0 {
			add(report, recommended, path, "sources", "sources should be a mapping or list of mappings")
		}
		for index, source := range sources {
			if value, _ := source["resource"].(string); strings.TrimSpace(value) == "" {
				add(report, recommended, path, fmt.Sprintf("sources[%d].resource", index), "source resource is required")
			}
			if value, exists := source["last_modified"]; exists {
				checkTime(path, fmt.Sprintf("sources[%d].last_modified", index), value, recommended, report)
			}
			checkUsageWindow(path, fmt.Sprintf("sources[%d].usage_window", index), source["usage_window"], recommended, report)
		}
	}
	checkUsageWindow(path, "usage_window", meta["usage_window"], recommended, report)
	if typeName == "Attested Computation" {
		if value, _ := meta["runtime"].(string); strings.TrimSpace(value) == "" {
			add(report, recommended, path, "computation.runtime", "Attested Computation should declare runtime")
		}
		if strict {
			_, hasFile := meta["computation"]
			if !hasFile && !hasComputationBody(body) {
				add(report, Error, path, "computation.body", "strict mode requires a computation path or a code block under # Computation")
			}
			checkReference(path, "executor", meta["executor"], true, report)
			checkReference(path, "attester", meta["attester"], false, report)
		}
	}
}

func strictLevel(strict bool) Level {
	if strict {
		return Error
	}
	return Warning
}

func checkActorEvent(path, rule string, event map[string]any, level Level, report *Report) {
	if value, _ := event["by"].(string); strings.TrimSpace(value) == "" {
		add(report, level, path, rule+".by", "actor is required")
	}
	value, exists := event["at"]
	if !exists {
		add(report, level, path, rule+".at", "timestamp is required")
	} else {
		checkTime(path, rule+".at", value, level, report)
	}
}

func checkTime(path, rule string, value any, level Level, report *Report) {
	var err error
	switch value := value.(type) {
	case string:
		_, err = time.Parse(time.RFC3339, value)
	case time.Time:
		// yaml.v3 resolves valid unquoted YAML timestamps into time.Time.
	default:
		err = fmt.Errorf("not a timestamp")
	}
	if err != nil {
		add(report, level, path, rule, "timestamp must be RFC3339 with an explicit UTC offset")
	}
}

func checkUsageWindow(path, rule string, value any, level Level, report *Report) {
	if value == nil {
		return
	}
	window, ok := mapping(value)
	if !ok {
		add(report, level, path, rule, "usage_window should be a mapping")
		return
	}
	for _, key := range []string{"from", "to"} {
		value, exists := window[key]
		if !exists {
			add(report, level, path, rule+"."+key, "timestamp is required")
			continue
		}
		checkTime(path, rule+"."+key, value, level, report)
	}
}

func checkReference(path, name string, value any, receipt bool, report *Report) {
	reference, ok := mapping(value)
	if !ok {
		add(report, Error, path, "computation."+name, name+" must be a mapping")
		return
	}
	if resource, _ := reference["resource"].(string); strings.TrimSpace(resource) == "" {
		add(report, Error, path, "computation."+name+".resource", name+" resource is required")
	}
	if receipt {
		values, ok := reference["receipt"].([]any)
		if !ok || len(values) == 0 {
			add(report, Error, path, "computation.executor.receipt", "executor receipt must list at least one field")
		}
	}
}

func hasComputationBody(source []byte) bool {
	root := goldmark.DefaultParser().Parse(text.NewReader(source))
	inSection := false
	found := false
	_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || found {
			return ast.WalkContinue, nil
		}
		switch value := node.(type) {
		case *ast.Heading:
			if value.Level == 1 {
				inSection = strings.EqualFold(strings.TrimSpace(string(value.Text(source))), "Computation")
			}
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			found = inSection
		}
		return ast.WalkContinue, nil
	})
	return found
}

func checkReserved(name, path string, body []byte, report *Report) {
	source := body
	root := goldmark.DefaultParser().Parse(text.NewReader(source))
	var headings []string
	_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if heading, ok := node.(*ast.Heading); ok {
				headings = append(headings, string(heading.Text(source)))
			}
		}
		return ast.WalkContinue, nil
	})
	if len(headings) == 0 {
		add(report, Error, path, "reserved.structure", name+" requires markdown headings")
	}
	if name == "log.md" {
		for _, heading := range headings[1:] {
			if !logDate.MatchString(strings.TrimSpace(heading)) {
				add(report, Error, path, "log.date", "log entry headings must use YYYY-MM-DD")
			}
		}
	}
}

func markdownLinks(source []byte) []string {
	root := goldmark.DefaultParser().Parse(text.NewReader(source))
	var links []string
	_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if link, ok := node.(*ast.Link); ok {
				links = append(links, string(link.Destination))
			}
		}
		return ast.WalkContinue, nil
	})
	return links
}

func checkGraph(root string, documents map[string]*document, strict bool, report *Report) {
	indexed := make(map[string]map[string]bool)
	paths := make([]string, 0, len(documents))
	for path := range documents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		doc := documents[path]
		if strings.EqualFold(filepath.Base(doc.rel), "index.md") {
			dir := filepath.ToSlash(filepath.Dir(doc.rel))
			indexed[dir] = make(map[string]bool)
			for _, link := range doc.links {
				if resolved, ok := resolveLink(root, doc.abs, link); ok {
					rel, _ := filepath.Rel(root, resolved)
					indexed[dir][filepath.ToSlash(rel)] = true
				}
			}
		}
		for _, link := range doc.links {
			resolved, local := resolveLink(root, doc.abs, link)
			if !local {
				continue
			}
			if _, err := os.Stat(resolved); err != nil {
				level := Warning
				if strict {
					level = Error
				}
				add(report, level, doc.rel, "link.broken", "link target does not exist: "+link)
			}
		}
	}
	if !strict {
		return
	}
	for _, rel := range paths {
		doc := documents[rel]
		if doc.reserved {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		entries, hasIndex := indexed[dir]
		if hasIndex && !entries[rel] {
			add(report, Error, rel, "index.coverage", "concept is not listed by its directory index.md")
		}
	}
}

func resolveLink(root, source, value string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || strings.HasPrefix(value, "#") {
		return "", false
	}
	path := parsed.Path
	if path == "" {
		return "", false
	}
	if strings.HasPrefix(path, "/") {
		path = filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(path, "/")))
	} else {
		path = filepath.Join(filepath.Dir(source), filepath.FromSlash(path))
	}
	return filepath.Clean(path), true
}

func mapping(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}

func mappings(value any) []map[string]any {
	if one, ok := mapping(value); ok {
		return []map[string]any{one}
	}
	values, _ := value.([]any)
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if item, ok := mapping(value); ok {
			result = append(result, item)
		}
	}
	return result
}

func add(report *Report, level Level, path, rule, message string) {
	report.Issues = append(report.Issues, Issue{Level: level, Path: filepath.ToSlash(path), Rule: rule, Message: message})
}
