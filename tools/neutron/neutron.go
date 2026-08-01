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

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	scanengine "github.com/chainreactors/aiscan/tools/scan/engine"
	"github.com/chainreactors/aiscan/tools/toolargs"
	"github.com/chainreactors/neutron/templates"
	sdkneutron "github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/association"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
	goflags "github.com/jessevdk/go-flags"
)

type Command struct {
	toolargs.Base
	engine *sdkneutron.Engine
	index  *association.Index
}

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
	Silent            bool     `long:"silent" description:"Only output matched loots"`
	Stats             bool     `long:"stats" description:"Include final scan statistics"`
	NoStats           bool     `long:"no-stats" description:"Disable final scan statistics"`
	MatchOnly         bool     `long:"match-only" description:"Only print matched templates"`
	AllResults        bool     `long:"all" description:"Print both matched and unmatched templates"`
	TemplateList      bool     `long:"template-list" description:"List selected templates and exit"`
	RestrictTemplates bool     `long:"restrict-templates" description:"Use only templates from --templates instead of merging with embedded templates"`
	Debug             bool     `long:"debug" description:"Enable debug logging"`
}

type neutronResult struct {
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
	c := &Command{engine: engine, index: index}
	c.InitLogger(nil)
	return c
}

func (c *Command) WithLogger(logger telemetry.Logger) *Command {
	c.InitLogger(logger)
	return c
}

func (c *Command) WithProxy(proxy string) *Command {
	c.Proxy = proxy
	scanengine.ApplyNeutronProxy(proxy)
	return c
}

func (c *Command) WithDataBus(bus *eventbus.Bus[output.ToolDataEvent]) *Command {
	c.DataBus = bus
	return c
}

func (c *Command) SetProxy(proxy string) {
	c.Base.SetProxy(proxy)
	scanengine.ApplyNeutronProxy(proxy)
}

func (c *Command) Name() string { return "neutron" }

func (c *Command) Usage() string {
	var options neutronFlags
	return toolargs.GoFlagsHelp("neutron", &options)
}

func (c *Command) QuickReference() string {
	return `### neutron — POC/vulnerability scanner (nuclei-style)
  -i, -u <target>  Target URL/host (repeatable, -l for list file)
  -t <path>        Extra template file or directory
  --finger <name>  Only run templates matching these fingerprints
  --tags <tags>    Filter templates by tag;  -s <sev>  Filter by severity
  --id <ids>       Run specific template IDs;  --template-list  List and exit
  -j               JSON Lines output
  Examples:
    neutron -i http://target.com
    neutron -i http://target.com --finger nginx --tags cve
    neutron -l targets.txt -s high,critical -j`
}

func (c *Command) Run(ctx context.Context, execution *commands.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("neutron", &err)
	args := execution.Args
	args = c.resolveRelativePaths(args)
	var flags neutronFlags
	parser := toolargs.NewGoFlagsParser("neutron", &flags)
	_, err = parser.ParseArgs(normalizeNucleiStyleArgs(args))
	if err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			fmt.Fprint(execution.Stdout, c.Usage()+"\n")
			return nil, nil
		}
		return nil, fmt.Errorf("neutron: %w", err)
	}
	if flags.Debug {
		restoreDebug := telemetry.ActivateDebug(c.Logger)
		defer restoreDebug()
		c.Logger.Debugf("neutron debug enabled")
	}
	if flags.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(flags.Timeout)*time.Second)
		defer cancel()
	}

	targets, err := readNeutronTargets(flags.Inputs, flags.Input, flags.ListFile)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 && !flags.TemplateList {
		return nil, fmt.Errorf("neutron: no input targets")
	}
	if c.engine == nil {
		return nil, fmt.Errorf("neutron: engine is not available")
	}
	if flags.Concurrency <= 0 {
		return nil, fmt.Errorf("neutron: --concurrency must be greater than 0")
	}
	if flags.RateLimit < 0 {
		return nil, fmt.Errorf("neutron: --rate-limit cannot be negative")
	}

	loadedTemplates, err := loadNeutronTemplatePaths(flags.Templates)
	if err != nil {
		return nil, err
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
		return nil, err
	}

	selected, filtered := selectNeutronTemplates(c.engine, c.index, opts)
	if filtered && len(selected) == 0 {
		return nil, fmt.Errorf("neutron: no templates selected")
	}
	if len(selected) == 0 {
		selected = nonNilSortedTemplates(c.engine.Get())
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("neutron: no templates available")
	}
	opts.Templates = selected
	opts.RestrictToTemplates = true

	if flags.TemplateList {
		result, wErr := c.writeOrReturn(flags.OutputFile, renderTemplateList(selected, flags.JSON || flags.JSONL))
		if result != "" {
			fmt.Fprint(execution.Stdout, result)
		}
		return nil, wErr
	}

	c.Logger.Infof("neutron action=testing targets=%d templates=%d concurrency=%d rate_limit=%d", len(targets), len(selected), flags.Concurrency, flags.RateLimit)
	summary := neutronSummary{Targets: len(targets), Templates: len(selected)}
	var sb strings.Builder
	jsonOutput := flags.JSON || flags.JSONL
	statsEnabled := (flags.Stats || !flags.NoStats) && !flags.NoStats
	results := make([]*sdktypes.TemplateResult, 0, len(targets)*len(selected))

	for _, target := range targets {
		targetOpts := opts
		targetOpts.Target = target
		resultCh, err := neutronExecuteStream(ctx, c.engine, c.index, targetOpts)
		if errors.Is(err, scanengine.ErrNoNeutronTemplates) {
			return nil, fmt.Errorf("neutron: no templates selected")
		}
		if err != nil {
			return nil, fmt.Errorf("neutron execute failed: %w", err)
		}
		for result := range resultCh {
			if result == nil {
				continue
			}
			summary.Executed++
			if result.Error() != nil {
				summary.Errors++
			}
			record := neutronResultFromExecution(target, result)
			results = append(results, result.TemplateResult(target))
			if record.Matched {
				summary.Matched++
				c.EmitDataCtx(ctx, "neutron", output.ToolDataVuln, target, &record)
			}
			if shouldPrintNeutronResult(record, flags) {
				line := formatNeutronResult(record, jsonOutput)
				sb.WriteString(line)
				fmt.Fprint(execution.Stdout, line)
			}
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("neutron: %w", ctx.Err())
		}
	}

	if statsEnabled && !flags.Silent && !jsonOutput {
		line := fmt.Sprintf("[neutron] completed targets=%d templates=%d executed=%d matched=%d errors=%d\n",
			summary.Targets, summary.Templates, summary.Executed, summary.Matched, summary.Errors)
		sb.WriteString(line)
		fmt.Fprint(execution.Stdout, line)
	}
	_, wErr := c.writeOrReturn(flags.OutputFile, sb.String())
	return results, wErr
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
	out = appendNonEmpty(out, inputs...)
	out = appendNonEmpty(out, input)
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

func shouldPrintNeutronResult(record neutronResult, flags neutronFlags) bool {
	if flags.AllResults {
		return true
	}
	if flags.MatchOnly || flags.Silent {
		return record.Matched
	}
	return record.Matched
}

func neutronResultFromExecution(target string, result *sdkneutron.ExecuteResult) neutronResult {
	record := neutronResult{
		Timestamp: time.Now().Format(time.RFC3339),
		Target:    target,
		Matched:   result.Matched(),
	}
	if tmpl := result.Template(); tmpl != nil {
		record.Template = tmpl.Id
		record.Name = tmpl.Info.Name
		record.Severity = tmpl.Info.Severity
		record.Tags = cleanTemplateTags(tmpl)
		record.Fingers = append([]string(nil), tmpl.Fingers...)
	}
	if opResult := result.Value(); opResult != nil {
		record.Extracts = append([]string(nil), opResult.OutputExtracts()...)
	}
	if err := result.Error(); err != nil {
		record.Error = err.Error()
	}
	return record
}

func formatNeutronResult(record neutronResult, jsonOutput bool) string {
	if jsonOutput {
		data, err := json.Marshal(record)
		if err != nil {
			return ""
		}
		return string(data) + "\n"
	}

	status := "VULN"
	if !record.Matched {
		status = "MISS"
	}
	var b strings.Builder
	b.WriteString("[")
	b.WriteString(status)
	b.WriteString("] ")
	b.WriteString(record.Target)
	if record.Template != "" {
		b.WriteString(" template=")
		b.WriteString(record.Template)
	}
	if record.Severity != "" {
		b.WriteString(" severity=")
		b.WriteString(record.Severity)
	}
	if record.Name != "" {
		b.WriteString(" name=")
		b.WriteString(strconv.Quote(record.Name))
	}
	if len(record.Extracts) > 0 {
		b.WriteString(" extracts=")
		b.WriteString(strconv.Quote(strings.Join(record.Extracts, ",")))
	}
	if record.Error != "" {
		b.WriteString(" error=")
		b.WriteString(strconv.Quote(record.Error))
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
var neutronFileFlags = map[string]bool{
	"-l": true, "--list": true,
	"-o": true, "--output": true,
	"-t": true, "--templates": true,
}

func (c *Command) resolveRelativePaths(args []string) []string {
	return toolargs.ResolveRelativePaths(args, neutronFileFlags, c.WorkDir)
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

func renderTemplateList(selected []*templates.Template, jsonOutput bool) string {
	var sb strings.Builder
	for _, tmpl := range selected {
		if tmpl == nil {
			continue
		}
		record := neutronResult{
			Template: tmpl.Id,
			Name:     tmpl.Info.Name,
			Severity: tmpl.Info.Severity,
			Tags:     cleanTemplateTags(tmpl),
			Fingers:  append([]string(nil), tmpl.Fingers...),
		}
		if jsonOutput {
			data, err := json.Marshal(record)
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
