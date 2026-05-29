package neutron

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	scanengine "github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/aiscan/pkg/util"
	"github.com/chainreactors/neutron/templates"
	sdkneutron "github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/association"
	goflags "github.com/jessevdk/go-flags"
)

type Command struct {
	engine  *sdkneutron.Engine
	index   *association.Index
	logger  telemetry.Logger
	proxy   string
	workDir string
}

func (c *Command) SetWorkDir(dir string) { c.workDir = dir }

type neutronFlags struct {
	Inputs            []string `short:"u" long:"target" description:"Target URL, host, or ip:port (can specify multiple)"`
	Input             string   `short:"i" long:"input" description:"Target URL, host, or ip:port (alias of --target)"`
	ListFile          string   `short:"l" long:"list" description:"File containing targets, one per line"`
	Templates         []string `short:"t" long:"templates" description:"Template file or directory (can specify multiple)"`
	TemplateID        []string `long:"id" description:"Run templates by id (comma-separated or repeated)"`
	ExcludeID         []string `long:"exclude-id" description:"Exclude templates by id (comma-separated or repeated)"`
	Fingers           []string `long:"finger" description:"Filter templates by fingerprint name"`
	Tags              []string `long:"tags" description:"Filter templates by tag (comma-separated or repeated)"`
	Tag               []string `long:"tag" description:"Filter templates by tag (alias of --tags)"`
	ExcludeTags       []string `long:"exclude-tags" description:"Exclude templates by tag (comma-separated or repeated)"`
	Severity          []string `short:"s" long:"severity" description:"Filter templates by severity: info, low, medium, high, critical"`
	ExcludeSeverity   []string `long:"exclude-severity" description:"Exclude templates by severity"`
	MaxPerFinger      int      `long:"max-per-finger" description:"Maximum templates selected per fingerprint"`
	Concurrency       int      `short:"c" long:"concurrency" description:"Template concurrency" default:"1"`
	RateLimit         int      `long:"rate-limit" description:"Maximum template executions per second"`
	Timeout           int      `long:"timeout" description:"Overall timeout in seconds"`
	OutputFile        string   `short:"o" long:"output" description:"Write output to file"`
	JSONL             bool     `long:"jsonl" description:"Output JSON Lines"`
	JSON              bool     `short:"j" long:"json" description:"Output JSON Lines (alias of --jsonl)"`
	Silent            bool     `long:"silent" description:"Only output matched findings"`
	Stats             bool     `long:"stats" description:"Include final scan statistics"`
	NoStats           bool     `long:"no-stats" description:"Disable final scan statistics"`
	MatchOnly         bool     `long:"match-only" description:"Only print matched templates"`
	AllResults        bool     `long:"all" description:"Print both matched and unmatched templates"`
	TemplateList      bool     `long:"template-list" description:"List selected templates and exit"`
	RestrictTemplates bool     `long:"restrict-templates" description:"Use only templates from --templates instead of merging with embedded templates"`
	Debug             bool     `long:"debug" description:"Enable debug logging"`
}

type neutronFinding struct {
	Timestamp string   `json:"timestamp,omitempty"`
	Target    string   `json:"target"`
	Matched   bool     `json:"matched"`
	Template  string   `json:"template"`
	Name      string   `json:"name,omitempty"`
	Severity  string   `json:"severity,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Fingers   []string `json:"fingers,omitempty"`
	Extracts  []string `json:"extracts,omitempty"`
	Error     string   `json:"error,omitempty"`
}

type neutronSummary struct {
	Targets   int
	Templates int
	Executed  int
	Matched   int
	Errors    int
}

func New(engine *sdkneutron.Engine, index *association.Index) *Command {
	return &Command{engine: engine, index: index, logger: telemetry.NopLogger()}
}

func (c *Command) WithLogger(logger telemetry.Logger) *Command {
	if logger != nil {
		c.logger = logger
	}
	return c
}

func (c *Command) WithProxy(proxy string) *Command {
	c.proxy = proxy
	scanengine.ApplyNeutronProxy(proxy)
	return c
}

func (c *Command) SetProxy(proxy string) {
	c.proxy = proxy
	scanengine.ApplyNeutronProxy(proxy)
}

func (c *Command) Name() string { return "neutron" }

func (c *Command) Usage() string {
	return `neutron - POC/vulnerability testing with nuclei-style options
Usage: neutron -u <target> [options]

Input:
  -u, --target       Target URL, host, or ip:port. Can specify multiple.
  -i, --input        Target URL, host, or ip:port (alias of --target).
  -l, --list         File containing targets, one per line.

Templates:
  -t, --templates    Template file or directory to run. Can specify multiple.
      --id           Run templates by id (comma-separated or repeated).
      --exclude-id   Exclude templates by id.
      --finger       Filter templates by fingerprint name.
      --tags, --tag  Filter templates by tag.
      --exclude-tags Exclude templates by tag.
  -s, --severity     Filter severity: info, low, medium, high, critical.
      --exclude-severity  Exclude severity.
      --max-per-finger    Maximum templates selected per fingerprint.

Rate and output:
  -c, --concurrency  Template concurrency (default: 1).
      --rate-limit, -rl  Maximum template executions per second.
      --timeout      Overall timeout in seconds.
  -o, --output       Write output to file.
  -j, --json         Output JSON Lines.
      --jsonl        Output JSON Lines.
      --silent       Only output matched findings.
      --all          Print matched and unmatched templates.
      --template-list  List selected templates and exit.
      --debug        Enable debug logging.

Examples:
  neutron -u http://target.com -s critical,high
  neutron -l targets.txt -tags cve,rce -c 10 --rate-limit 20
  neutron -u 10.0.0.1:8080 --finger nginx --max-per-finger 20
  neutron -u http://target.com -t ./pocs --id shiro-detect -j -o findings.jsonl`
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	args = c.resolveRelativePaths(args)
	var flags neutronFlags
	parser := goflags.NewParser(&flags, goflags.Default&^goflags.PrintErrors)
	_, err := parser.ParseArgs(normalizeNucleiStyleArgs(args))
	if err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			return c.Usage() + "\n", nil
		}
		return "", fmt.Errorf("neutron: %w", err)
	}
	if flags.Debug {
		restoreDebug := telemetry.ActivateDebug(c.logger)
		defer restoreDebug()
		c.logger.Debugf("neutron debug enabled")
	}
	if flags.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(flags.Timeout)*time.Second)
		defer cancel()
	}

	targets, err := readNeutronTargets(flags.Inputs, flags.Input, flags.ListFile)
	if err != nil {
		return "", err
	}
	if len(targets) == 0 && !flags.TemplateList {
		return "", fmt.Errorf("neutron: no input targets")
	}
	if c.engine == nil {
		return "", fmt.Errorf("neutron: engine is not available")
	}
	if flags.Concurrency <= 0 {
		return "", fmt.Errorf("neutron: --concurrency must be greater than 0")
	}
	if flags.RateLimit < 0 {
		return "", fmt.Errorf("neutron: --rate-limit cannot be negative")
	}

	loadedTemplates, err := loadNeutronTemplatePaths(flags.Templates)
	if err != nil {
		return "", err
	}

	opts := neutronExecuteOptions{
		Templates:           loadedTemplates,
		RestrictToTemplates: flags.RestrictTemplates || len(loadedTemplates) > 0,
		Fingers:             expandCSV(flags.Fingers),
		Tags:                expandCSV(append(flags.Tags, flags.Tag...)),
		ExcludeTags:         expandCSV(flags.ExcludeTags),
		Severities:          expandCSV(flags.Severity),
		ExcludeSeverities:   expandCSV(flags.ExcludeSeverity),
		IDs:                 expandCSV(flags.TemplateID),
		ExcludeIDs:          expandCSV(flags.ExcludeID),
		MaxPerFinger:        flags.MaxPerFinger,
		Concurrency:         flags.Concurrency,
		RateLimit:           flags.RateLimit,
		TemplateList:        flags.TemplateList,
		Debug:               flags.Debug,
	}
	if err := validateNeutronSeverities(opts.Severities, opts.ExcludeSeverities); err != nil {
		return "", err
	}

	selected, filtered := selectNeutronTemplates(c.engine, c.index, opts)
	if filtered && len(selected) == 0 {
		return "", fmt.Errorf("neutron: no templates selected")
	}
	if len(selected) == 0 {
		selected = nonNilSortedTemplates(c.engine.Get())
	}
	if len(selected) == 0 {
		return "", fmt.Errorf("neutron: no templates available")
	}
	opts.Templates = selected
	opts.RestrictToTemplates = true

	if flags.TemplateList {
		return c.writeOrReturn(flags.OutputFile, renderTemplateList(selected, flags.JSON || flags.JSONL))
	}

	c.logger.Infof("neutron action=testing targets=%d templates=%d concurrency=%d rate_limit=%d", len(targets), len(selected), flags.Concurrency, flags.RateLimit)
	summary := neutronSummary{Targets: len(targets), Templates: len(selected)}
	var sb strings.Builder
	jsonOutput := flags.JSON || flags.JSONL
	statsEnabled := (flags.Stats || !flags.NoStats) && !flags.NoStats

	for _, target := range targets {
		targetOpts := opts
		targetOpts.Target = target
		resultCh, err := neutronExecuteStream(ctx, c.engine, c.index, targetOpts)
		if errors.Is(err, scanengine.ErrNoNeutronTemplates) {
			return "", fmt.Errorf("neutron: no templates selected")
		}
		if err != nil {
			return "", fmt.Errorf("neutron execute failed: %w", err)
		}
		for result := range resultCh {
			if result == nil {
				continue
			}
			summary.Executed++
			if result.Error() != nil {
				summary.Errors++
			}
			finding := findingFromResult(target, result)
			if finding.Matched {
				summary.Matched++
			}
			if shouldPrintFinding(finding, flags) {
				sb.WriteString(formatFinding(finding, jsonOutput))
			}
		}
		if ctx.Err() != nil {
			return sb.String(), fmt.Errorf("neutron: %w", ctx.Err())
		}
	}

	if statsEnabled && !flags.Silent && !jsonOutput {
		sb.WriteString(fmt.Sprintf("[neutron] completed targets=%d templates=%d executed=%d matched=%d errors=%d\n",
			summary.Targets, summary.Templates, summary.Executed, summary.Matched, summary.Errors))
	}
	return c.writeOrReturn(flags.OutputFile, sb.String())
}

func normalizeNucleiStyleArgs(args []string) []string {
	known := map[string]struct{}{
		"-target": {}, "-list": {}, "-templates": {}, "-id": {}, "-exclude-id": {},
		"-finger": {}, "-tags": {}, "-tag": {}, "-exclude-tags": {}, "-severity": {},
		"-exclude-severity": {}, "-max-per-finger": {}, "-concurrency": {}, "-rate-limit": {},
		"-timeout": {}, "-output": {}, "-json": {}, "-jsonl": {}, "-silent": {},
		"-stats": {}, "-no-stats": {}, "-match-only": {}, "-all": {}, "-template-list": {},
		"-restrict-templates": {},
		"-debug":              {},
		"-rl":                 {},
		"-etags":              {},
		"-eid":                {},
		"-es":                 {},
		"-tl":                 {},
	}
	aliases := map[string]string{
		"-rl":    "-rate-limit",
		"-etags": "-exclude-tags",
		"-eid":   "-exclude-id",
		"-es":    "-exclude-severity",
		"-tl":    "-template-list",
	}
	out := append([]string(nil), args...)
	for i, arg := range out {
		key, value, hasValue := strings.Cut(arg, "=")
		if _, ok := known[key]; ok {
			if alias, ok := aliases[key]; ok {
				key = alias
			}
			out[i] = "-" + key
			if hasValue {
				out[i] += "=" + value
			}
		}
	}
	return out
}

func (c *Command) writeOrReturn(path, content string) (string, error) {
	if path == "" {
		return content, nil
	}
	path = filepath.Clean(path)
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("neutron output: create directory: %w", err)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("neutron output: %w", err)
	}
	return content, nil
}

func readNeutronTargets(inputs []string, input, listFile string) ([]string, error) {
	var out []string
	out = util.AppendNonEmpty(out, inputs...)
	out = util.AppendNonEmpty(out, input)
	if listFile == "" {
		return out, nil
	}

	f, err := os.Open(listFile)
	if err != nil {
		return nil, fmt.Errorf("neutron: open target list: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, scanner.Err()
}

func loadNeutronTemplatePaths(paths []string) ([]*templates.Template, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	cfg := sdkneutron.NewConfig()
	engine, err := sdkneutron.NewEngine(cfg.WithTemplates([]*templates.Template{minimalCompilableTemplate()}))
	if err != nil {
		return nil, fmt.Errorf("neutron: initialize template loader: %w", err)
	}
	defer engine.Close()
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if err := engine.AddPocsFile(path); err != nil {
			return nil, fmt.Errorf("neutron: load templates %s: %w", path, err)
		}
	}

	var loaded []*templates.Template
	for _, tmpl := range engine.Get() {
		if tmpl == nil || tmpl.Id == minimalTemplateID {
			continue
		}
		loaded = append(loaded, tmpl)
	}
	return nonNilSortedTemplates(loaded), nil
}

const minimalTemplateID = "__aiscan_neutron_loader__"

func minimalCompilableTemplate() *templates.Template {
	return &templates.Template{
		Id: minimalTemplateID,
		Info: templates.Info{
			Name:     minimalTemplateID,
			Severity: "info",
		},
	}
}

func expandCSV(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func validateNeutronSeverities(groups ...[]string) error {
	valid := map[string]struct{}{
		"info": {}, "low": {}, "medium": {}, "high": {}, "critical": {}, "unknown": {},
	}
	for _, values := range groups {
		for _, value := range values {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" {
				continue
			}
			if _, ok := valid[value]; !ok {
				return fmt.Errorf("neutron: invalid severity %q", value)
			}
		}
	}
	return nil
}

func shouldPrintFinding(finding neutronFinding, flags neutronFlags) bool {
	if flags.AllResults {
		return true
	}
	if flags.MatchOnly || flags.Silent {
		return finding.Matched
	}
	return finding.Matched
}

func findingFromResult(target string, result *sdkneutron.ExecuteResult) neutronFinding {
	finding := neutronFinding{
		Timestamp: time.Now().Format(time.RFC3339),
		Target:    target,
		Matched:   result.Matched(),
	}
	if tmpl := result.Template(); tmpl != nil {
		finding.Template = tmpl.Id
		finding.Name = tmpl.Info.Name
		finding.Severity = tmpl.Info.Severity
		finding.Tags = cleanTemplateTags(tmpl)
		finding.Fingers = append([]string(nil), tmpl.Fingers...)
	}
	if opResult := result.Result(); opResult != nil && opResult.Result != nil {
		finding.Extracts = append([]string(nil), opResult.Result.OutputExtracts...)
	}
	if err := result.Error(); err != nil {
		finding.Error = err.Error()
	}
	return finding
}

func formatFinding(finding neutronFinding, jsonOutput bool) string {
	if jsonOutput {
		data, err := json.Marshal(finding)
		if err != nil {
			return ""
		}
		return string(data) + "\n"
	}

	status := "VULN"
	if !finding.Matched {
		status = "MISS"
	}
	var b strings.Builder
	b.WriteString("[")
	b.WriteString(status)
	b.WriteString("] ")
	b.WriteString(finding.Target)
	if finding.Template != "" {
		b.WriteString(" template=")
		b.WriteString(finding.Template)
	}
	if finding.Severity != "" {
		b.WriteString(" severity=")
		b.WriteString(finding.Severity)
	}
	if finding.Name != "" {
		b.WriteString(" name=")
		b.WriteString(strconv.Quote(finding.Name))
	}
	if len(finding.Extracts) > 0 {
		b.WriteString(" extracts=")
		b.WriteString(strconv.Quote(strings.Join(finding.Extracts, ",")))
	}
	if finding.Error != "" {
		b.WriteString(" error=")
		b.WriteString(strconv.Quote(finding.Error))
	}
	b.WriteByte('\n')
	return b.String()
}

func cleanTemplateTags(tmpl *templates.Template) []string {
	var tags []string
	for _, tag := range tmpl.GetTags() {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}


// resolveRelativePaths resolves relative file arguments against workDir.
func (c *Command) resolveRelativePaths(args []string) []string {
	if c.workDir == "" {
		return args
	}
	fileFlags := map[string]bool{
		"-l": true, "--list": true,
		"-o": true, "--output": true,
		"-t": true, "--templates": true,
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if key, value, ok := strings.Cut(arg, "="); ok {
			if fileFlags[key] {
				out = append(out, key+"="+c.resolvePath(value))
				continue
			}
			out = append(out, arg)
			continue
		}
		if fileFlags[arg] && i+1 < len(args) {
			out = append(out, arg)
			i++
			out = append(out, c.resolvePath(args[i]))
			continue
		}
		out = append(out, arg)
	}
	return out
}

func (c *Command) resolvePath(value string) string {
	if value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "-") {
		return value
	}
	return filepath.Join(c.workDir, value)
}

func renderTemplateList(selected []*templates.Template, jsonOutput bool) string {
	var sb strings.Builder
	for _, tmpl := range selected {
		if tmpl == nil {
			continue
		}
		finding := neutronFinding{
			Template: tmpl.Id,
			Name:     tmpl.Info.Name,
			Severity: tmpl.Info.Severity,
			Tags:     cleanTemplateTags(tmpl),
			Fingers:  append([]string(nil), tmpl.Fingers...),
		}
		if jsonOutput {
			data, err := json.Marshal(finding)
			if err == nil {
				sb.Write(data)
				sb.WriteByte('\n')
			}
			continue
		}
		sb.WriteString(tmpl.Id)
		if tmpl.Info.Severity != "" {
			sb.WriteString(" [")
			sb.WriteString(tmpl.Info.Severity)
			sb.WriteString("]")
		}
		if tmpl.Info.Name != "" {
			sb.WriteString(" ")
			sb.WriteString(tmpl.Info.Name)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}
