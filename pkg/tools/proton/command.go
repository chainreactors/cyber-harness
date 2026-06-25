package proton

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	"github.com/chainreactors/neutron/operators"
	"github.com/chainreactors/neutron/protocols"
	"github.com/chainreactors/proton/proton/file"
	sdkproton "github.com/chainreactors/sdk/proton"
	goflags "github.com/jessevdk/go-flags"
)

type Command struct {
	toolargs.Base
	stdinFile        string
	resourceProvider func(string) []byte
}

func New() *Command {
	c := &Command{}
	c.InitLogger(nil)
	return c
}

func (c *Command) WithLogger(logger telemetry.Logger) *Command {
	c.InitLogger(logger)
	return c
}

func (c *Command) WithProxy(proxy string) *Command {
	c.Proxy = proxy
	return c
}

func (c *Command) WithResourceProvider(provider func(string) []byte) *Command {
	c.resourceProvider = provider
	return c
}

func (c *Command) SetStdinFile(path string) { c.stdinFile = path }
func (c *Command) Name() string             { return "proton" }

func (c *Command) Usage() string {
	return `proton - sensitive information scanner (nuclei-style)
Usage: proton -i <path> [options]
       <command> | proton [options]

Input:
  -i, --input        Target file or directory to scan
  -l, --list         File containing targets, one per line
  -e, --expression   Regex pattern to search directly (can specify multiple)
      --ext          File extensions for -e mode (.go,.py)

Templates:
  -t, --templates    Template file or directory (can specify multiple)
  -c, --category     Builtin categories: keys, spray, all (default: keys)
      --id           Filter rules by ID (can specify multiple)
      --exclude-id   Exclude rules by ID
      --tags         Filter rules by tag (can specify multiple)
      --exclude-tags Exclude rules by tag
  -s, --severity     Filter severity: critical,high,medium,low,info
      --exclude-severity  Exclude severity
      --template-list     List selected rules and exit

Output:
  -o, --output       Write output to file
  -j, --json         JSON Lines output
      --stats        Include final scan statistics (default)
      --no-stats     Disable final scan statistics
      --silent       Only output findings, no stats

Scan control:
      --bin          Include binary files (default: text-only)
      --timeout      Overall timeout in seconds
      --debug        Enable debug logging

Pipe usage:
  curl -s http://target/api | proton
  cat config.yaml | proton -e "password|secret|token"
  spray -u http://target | proton --tags spray

Examples:
  proton -i /path/to/project
  proton -i . -s high
  proton -l paths.txt -c keys,spray
  proton -i . --tags cloud --exclude-id ip-with-port
  proton -i . -e "AKIA[0-9A-Z]{16}"
  proton -t ./custom-rules -i .
  proton --template-list -c keys`
}

type protonFlags struct {
	// Input
	Input       string   `short:"i" long:"input" description:"target file or directory"`
	ListFile    string   `short:"l" long:"list" description:"file containing targets, one per line"`
	Expressions []string `short:"e" long:"expression" description:"regex pattern to search"`
	ExtFilter   string   `long:"ext" description:"file extensions for -e mode (.go,.py)"`

	// Template selection (aligned with neutron)
	Templates       []string `short:"t" long:"templates" description:"template file or directory"`
	Categories      []string `short:"c" long:"category" description:"builtin categories" default:"keys"`
	TemplateIDs     []string `long:"id" description:"filter rules by ID"`
	ExcludeIDs      []string `long:"exclude-id" description:"exclude rules by ID"`
	Tags            []string `long:"tags" description:"filter rules by tag"`
	ExcludeTags     []string `long:"exclude-tags" description:"exclude rules by tag"`
	Severity        []string `short:"s" long:"severity" description:"filter severity"`
	ExcludeSeverity []string `long:"exclude-severity" description:"exclude severity"`
	TemplateList    bool     `long:"template-list" description:"list selected rules and exit"`

	// Output (aligned with neutron)
	OutputFile string `short:"o" long:"output" description:"write output to file"`
	JSON       bool   `short:"j" long:"json" description:"JSON Lines output"`
	Stats      bool   `long:"stats" description:"include final scan statistics"`
	NoStats    bool   `long:"no-stats" description:"disable final scan statistics"`
	Silent     bool   `long:"silent" description:"only output findings, no stats"`

	// Scan control
	Bin     bool `long:"bin" description:"include binary files"`
	Timeout int  `long:"timeout" description:"overall timeout in seconds"`
	Debug   bool `long:"debug" description:"enable debug logging"`
}

func (c *Command) Execute(ctx context.Context, args []string) error {
	args = c.resolveRelativePaths(args)
	var flags protonFlags
	parser := goflags.NewParser(&flags, goflags.Default&^goflags.PrintErrors)
	remaining, err := parser.ParseArgs(normalizeShortFlags(args))
	if err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			fmt.Fprint(commands.Output, c.Usage()+"\n")
			return nil
		}
		return fmt.Errorf("proton: %w", err)
	}

	if flags.Debug {
		restoreDebug := telemetry.ActivateDebug(c.Logger)
		defer restoreDebug()
		c.Logger.Debugf("proton debug enabled")
	}
	if flags.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(flags.Timeout)*time.Second)
		defer cancel()
	}

	// --- Build engine via SDK (template loading + filtering) ---
	cfg := sdkproton.NewConfig().
		WithTextOnly(!flags.Bin).
		WithCategories(flags.Categories...).
		WithTemplatePaths(flags.Templates...).
		WithTags(flags.Tags...).
		WithExcludeTags(flags.ExcludeTags...).
		WithIDs(flags.TemplateIDs...).
		WithExcludeIDs(flags.ExcludeIDs...)

	if c.resourceProvider != nil {
		cfg.WithResourceProvider(c.resourceProvider)
	}

	if len(flags.Expressions) > 0 {
		rule, exprErr := buildExpressionRule(flags.Expressions, flags.ExtFilter, !flags.Bin)
		if exprErr != nil {
			return fmt.Errorf("proton: %w", exprErr)
		}
		cfg.WithRules(rule)
	}

	engine, engineErr := sdkproton.NewEngine(cfg)
	if engineErr != nil {
		return fmt.Errorf("proton: %w", engineErr)
	}
	scanner := engine.Scanner()
	if scanner == nil || len(scanner.Groups) == 0 {
		return fmt.Errorf("proton: no rules loaded (check -c, -t, or -e flags)")
	}

	// --- Template list mode ---
	if flags.TemplateList {
		return c.renderTemplateList(scanner, flags.JSON)
	}

	// --- Resolve inputs ---
	inputs, err := readInputs(flags.Input, flags.ListFile, remaining)
	if err != nil {
		return fmt.Errorf("proton: %w", err)
	}
	if len(inputs) == 0 && c.stdinFile != "" {
		inputs = append(inputs, c.stdinFile)
		defer func() {
			os.Remove(c.stdinFile)
			c.stdinFile = ""
		}()
	}
	if len(inputs) == 0 && len(flags.Expressions) == 0 {
		return fmt.Errorf("proton: target required (-i <path>, -l <file>, -e <regex>, or pipe: <cmd> | proton)")
	}
	if len(inputs) == 0 {
		inputs = []string{"."}
	}

	// --- Scan ---
	sevFilter := buildSeverityFilter(flags.Severity, flags.ExcludeSeverity)
	var seen sync.Map
	var findingCount int64

	var fileOut *os.File
	if flags.OutputFile != "" {
		f, fErr := os.Create(flags.OutputFile)
		if fErr != nil {
			return fmt.Errorf("proton: %w", fErr)
		}
		defer f.Close()
		fileOut = f
	}

	callback := func(uf file.Finding) {
		if !sevFilter.allow(uf.Severity) {
			return
		}
		key := uf.TemplateID + "|" + uf.FilePath
		if _, loaded := seen.LoadOrStore(key, true); loaded {
			return
		}
		atomic.AddInt64(&findingCount, 1)
		writeFinding(commands.Output, uf, flags.JSON, inputs[0])
		if fileOut != nil {
			writeFinding(fileOut, uf, flags.JSON, inputs[0])
		}
	}

	c.Logger.Infof("proton action=scanning targets=%d rules=%d", len(inputs), scanner.Stats.Rules)

	for _, input := range inputs {
		info, statErr := os.Stat(input)
		if statErr != nil {
			c.Logger.Warnf("proton: skip %s: %v", input, statErr)
			continue
		}
		if info.IsDir() {
			walkAndScan(ctx, scanner, input, callback)
		} else {
			scanSingleFile(scanner, input, callback)
		}
	}

	// --- Summary ---
	statsEnabled := flags.Stats || (!flags.NoStats && !flags.Silent && !flags.JSON)
	if statsEnabled {
		count := atomic.LoadInt64(&findingCount)
		fileCount := atomic.LoadInt64(&scanner.Stats.Files)
		ruleCount := scanner.Stats.Rules
		if count > 0 {
			fmt.Fprintf(commands.Output, "\n[proton] %d findings | %d rules | %d files\n", count, ruleCount, fileCount)
		} else {
			fmt.Fprintf(commands.Output, "[proton] no findings | %d rules | %d files\n", ruleCount, fileCount)
		}
	}
	return nil
}

// --- template list ---

func (c *Command) renderTemplateList(scanner *file.Scanner, jsonOutput bool) error {
	var sb strings.Builder
	count := 0
	for _, group := range scanner.Groups {
		for _, ref := range group.Templates {
			count++
			if jsonOutput {
				data, _ := json.Marshal(map[string]string{
					"id":       ref.ID,
					"name":     ref.Name,
					"severity": ref.Severity,
				})
				sb.Write(data)
				sb.WriteByte('\n')
				continue
			}
			sb.WriteString(ref.ID)
			if ref.Severity != "" {
				sb.WriteString(" [")
				sb.WriteString(ref.Severity)
				sb.WriteString("]")
			}
			if ref.Name != "" {
				sb.WriteString(" ")
				sb.WriteString(ref.Name)
			}
			sb.WriteByte('\n')
		}
	}
	fmt.Fprint(commands.Output, sb.String())
	if !jsonOutput {
		fmt.Fprintf(commands.Output, "\nTotal: %d rules\n", count)
	}
	return nil
}

// --- path resolution (same pattern as neutron) ---

func readInputs(input, listFile string, remaining []string) ([]string, error) {
	var out []string
	if input != "" {
		out = append(out, input)
	}
	for _, r := range remaining {
		r = strings.TrimSpace(r)
		if r != "" {
			out = append(out, r)
		}
	}
	if listFile == "" {
		return out, nil
	}
	f, err := os.Open(listFile)
	if err != nil {
		return nil, fmt.Errorf("open target list: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

var protonFileFlags = map[string]bool{
	"-i": true, "--input": true,
	"-l": true, "--list": true,
	"-o": true, "--output": true,
	"-t": true, "--templates": true,
}

func (c *Command) resolveRelativePaths(args []string) []string {
	return toolargs.ResolveRelativePaths(args, protonFileFlags, c.WorkDir)
}

// --- output ---

func writeFinding(w interface{ Write([]byte) (int, error) }, f file.Finding, jsonMode bool, baseDir string) {
	if jsonMode {
		data, _ := json.Marshal(f)
		fmt.Fprintln(w, string(data))
		return
	}
	relPath := f.FilePath
	if r, err := filepath.Rel(baseDir, f.FilePath); err == nil {
		relPath = r
	}
	fmt.Fprintf(w, "[%s] [%s] [%s] %s\n", f.TemplateName, f.Severity, f.TemplateID, relPath)
	for name, events := range f.Matches {
		for _, ev := range events {
			val := truncate(ev.Value, 200)
			fmt.Fprintf(w, "   [%s] [L%d] %s\n", name, ev.Line, val)
		}
	}
	for _, ev := range f.Extracts {
		val := truncate(ev.Value, 200)
		fmt.Fprintf(w, "   [L%d] %s\n", ev.Line, val)
	}
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max]) + "..."
	}
	return s
}

// --- scanning ---

func scanSingleFile(scanner *file.Scanner, path string, callback func(file.Finding)) {
	for _, group := range scanner.Groups {
		contents := scanner.ReadFile(path, group)
		for _, c := range contents {
			findings := scanner.ScanData(c.Data, c.Label, group)
			atomic.AddInt64(&scanner.Stats.Files, 1)
			atomic.AddInt64(&scanner.Stats.Bytes, int64(len(c.Data)))
			for _, f := range findings {
				atomic.AddInt64(&scanner.Stats.Findings, 1)
				callback(f)
			}
		}
	}
}

func walkAndScan(ctx context.Context, scanner *file.Scanner, target string, callback func(file.Finding)) {
	numWorkers := runtime.NumCPU()
	if numWorkers > 8 {
		numWorkers = 8
	}
	type job struct {
		path  string
		group *file.ScanGroup
	}
	jobCh := make(chan job, numWorkers*256)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				if ctx.Err() != nil {
					return
				}
				contents := scanner.ReadFile(j.path, j.group)
				for _, c := range contents {
					findings := scanner.ScanData(c.Data, c.Label, j.group)
					atomic.AddInt64(&scanner.Stats.Files, 1)
					atomic.AddInt64(&scanner.Stats.Bytes, int64(len(c.Data)))
					if len(findings) > 0 {
						atomic.AddInt64(&scanner.Stats.Findings, int64(len(findings)))
						mu.Lock()
						for _, f := range findings {
							callback(f)
						}
						mu.Unlock()
					}
				}
			}
		}()
	}

	if walkErr := filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if file.ShouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		ext := filepath.Ext(path)
		if file.ShouldDenyExt(ext) {
			return nil
		}
		for _, group := range scanner.Groups {
			if !group.MatchesFile(path, ext) {
				continue
			}
			jobCh <- job{path: path, group: group}
		}
		return nil
	}); walkErr != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "proton: walk %s: %v\n", target, walkErr)
	}
	close(jobCh)
	wg.Wait()
}

// --- helpers ---

type severityFilter struct {
	include map[string]bool
	exclude map[string]bool
}

func buildSeverityFilter(include, exclude []string) severityFilter {
	return severityFilter{
		include: toSet(include),
		exclude: toSet(exclude),
	}
}

func (f severityFilter) allow(sev string) bool {
	s := strings.ToLower(sev)
	if len(f.exclude) > 0 && f.exclude[s] {
		return false
	}
	if len(f.include) > 0 {
		return f.include[s]
	}
	return true
}

func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]bool)
	for _, item := range items {
		for _, s := range strings.Split(item, ",") {
			s = strings.TrimSpace(strings.ToLower(s))
			if s != "" {
				m[s] = true
			}
		}
	}
	return m
}

func buildExpressionRule(expressions []string, extFilter string, textOnly bool) (sdkproton.Rule, error) {
	execOpts := &protocols.ExecuterOptions{
		Options: &protocols.Options{TextOnly: textOnly},
	}
	exts := []string{"all"}
	if extFilter != "" {
		exts = nil
		for _, ext := range strings.Split(extFilter, ",") {
			ext = strings.TrimSpace(ext)
			if ext != "" {
				if !strings.HasPrefix(ext, ".") {
					ext = "." + ext
				}
				exts = append(exts, ext)
			}
		}
	}
	req := &file.Request{Extensions: exts}
	var extractors []*operators.Extractor
	for _, expr := range expressions {
		extractors = append(extractors, &operators.Extractor{
			Type:  "regex",
			Regex: []string{expr},
		})
	}
	req.Extractors = extractors
	if err := req.Compile(execOpts); err != nil {
		return sdkproton.Rule{}, fmt.Errorf("compile expression: %w", err)
	}
	return sdkproton.Rule{
		ID: "expression", Name: "Expression",
		Severity: "info", Requests: []*file.Request{req},
	}, nil
}

var protonKnownFlags = map[string]struct{}{
	"-input": {}, "-list": {}, "-expression": {}, "-ext": {},
	"-templates": {}, "-category": {}, "-id": {}, "-exclude-id": {},
	"-tags": {}, "-exclude-tags": {}, "-severity": {}, "-exclude-severity": {},
	"-template-list": {}, "-output": {}, "-json": {},
	"-stats": {}, "-no-stats": {}, "-silent": {},
	"-bin": {}, "-timeout": {}, "-debug": {},
}

func normalizeShortFlags(args []string) []string {
	return toolargs.NormalizeFlags(args, protonKnownFlags, toolargs.CommonAliases)
}
