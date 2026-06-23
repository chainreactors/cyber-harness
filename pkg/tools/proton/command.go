package proton

import (
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

	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/neutron/operators"
	"github.com/chainreactors/neutron/protocols"
	"github.com/chainreactors/proton/proton/file"
	sdkproton "github.com/chainreactors/sdk/proton"
	goflags "github.com/jessevdk/go-flags"
)

type Command struct {
	logger    telemetry.Logger
	workDir   string
	stdinFile string
}

func New() *Command {
	return &Command{logger: telemetry.NopLogger()}
}

func (c *Command) WithLogger(logger telemetry.Logger) *Command {
	if logger != nil {
		c.logger = logger
	}
	return c
}

func (c *Command) SetWorkDir(dir string)    { c.workDir = dir }
func (c *Command) SetStdinFile(path string) { c.stdinFile = path }
func (c *Command) Name() string             { return "proton" }

func (c *Command) Usage() string {
	return `proton - sensitive information scanner (nuclei-style)
Usage: proton -i <path> [options]
       <command> | proton [options]

Input:
  -i, --input        Target file or directory to scan
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
  -q, --quiet        Only print findings, no summary

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
  proton -i . -s critical,high
  proton -i . --tags cloud --exclude-id ip-with-port
  proton -i . -e "AKIA[0-9A-Z]{16}"
  proton -t ./custom-rules -i .
  proton --template-list -c keys`
}

type protonFlags struct {
	Input       string   `short:"i" long:"input" description:"target file or directory"`
	Expressions []string `short:"e" long:"expression" description:"regex pattern to search"`
	ExtFilter   string   `long:"ext" description:"file extensions for -e mode (.go,.py)"`

	Templates       []string `short:"t" long:"templates" description:"template file or directory"`
	Categories      []string `short:"c" long:"category" description:"builtin categories" default:"keys"`
	TemplateIDs     []string `long:"id" description:"filter rules by ID"`
	ExcludeIDs      []string `long:"exclude-id" description:"exclude rules by ID"`
	Tags            []string `long:"tags" description:"filter rules by tag"`
	ExcludeTags     []string `long:"exclude-tags" description:"exclude rules by tag"`
	Severity        []string `short:"s" long:"severity" description:"filter severity"`
	ExcludeSeverity []string `long:"exclude-severity" description:"exclude severity"`
	TemplateList    bool     `long:"template-list" description:"list selected rules and exit"`

	OutputFile string `short:"o" long:"output" description:"write output to file"`
	JSON       bool   `short:"j" long:"json" description:"JSON Lines output"`
	Quiet      bool   `short:"q" long:"quiet" description:"only print findings"`

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
		restoreDebug := telemetry.ActivateDebug(c.logger)
		defer restoreDebug()
		c.logger.Debugf("proton debug enabled")
	}
	if flags.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(flags.Timeout)*time.Second)
		defer cancel()
	}

	// --- Build engine via SDK (template loading + filtering) ---
	cfg := sdkproton.NewConfig().
		WithTextOnly(!flags.Bin).
		WithResourceProvider(resources.ProtonConfig).
		WithCategories(flags.Categories...).
		WithTemplatePaths(flags.Templates...).
		WithTags(flags.Tags...).
		WithExcludeTags(flags.ExcludeTags...).
		WithIDs(flags.TemplateIDs...).
		WithExcludeIDs(flags.ExcludeIDs...)

	if len(flags.Expressions) > 0 {
		rule, exprErr := buildExpressionRule(flags.Expressions, flags.ExtFilter, !flags.Bin)
		if exprErr != nil {
			return fmt.Errorf("proton: %v", exprErr)
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
		fmt.Fprintf(commands.Output, "[proton] %d rules loaded\n", scanner.Stats.Rules)
		return nil
	}

	// --- Resolve input ---
	input := c.resolveInput(flags.Input, remaining)
	if input == "" && c.stdinFile != "" {
		input = c.stdinFile
		defer func() {
			os.Remove(c.stdinFile)
			c.stdinFile = ""
		}()
	}
	if input == "" && len(flags.Expressions) == 0 {
		return fmt.Errorf("proton: target required (-i <path>, -e <regex>, or pipe: <cmd> | proton)")
	}

	// --- Scan ---
	sevFilter := buildSeverityFilter(flags.Severity, flags.ExcludeSeverity)
	seen := make(map[string]bool)
	var findingCount int64

	var fileOut *os.File
	if flags.OutputFile != "" {
		f, fErr := os.Create(flags.OutputFile)
		if fErr != nil {
			return fmt.Errorf("proton: %v", fErr)
		}
		defer f.Close()
		fileOut = f
	}

	callback := func(uf file.Finding) {
		if !sevFilter.allow(uf.Severity) {
			return
		}
		key := uf.TemplateID + "|" + uf.FilePath
		if seen[key] {
			return
		}
		seen[key] = true
		atomic.AddInt64(&findingCount, 1)
		writeFinding(commands.Output, uf, flags.JSON, input)
		if fileOut != nil {
			writeFinding(fileOut, uf, flags.JSON, input)
		}
	}

	if input != "" {
		info, statErr := os.Stat(input)
		if statErr != nil {
			return fmt.Errorf("proton: %v", statErr)
		}
		if info.IsDir() {
			walkAndScan(ctx, scanner, input, callback)
		} else {
			scanSingleFile(scanner, input, callback)
		}
	}

	if !flags.Quiet {
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

// --- path resolution (same pattern as neutron) ---

func (c *Command) resolveInput(input string, remaining []string) string {
	if input == "" && len(remaining) > 0 {
		input = remaining[0]
	}
	if input != "" && !filepath.IsAbs(input) && c.workDir != "" {
		input = filepath.Join(c.workDir, input)
	}
	return input
}

func (c *Command) resolveRelativePaths(args []string) []string {
	if c.workDir == "" {
		return args
	}
	fileFlags := map[string]bool{
		"-i": true, "--input": true,
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
	if len(s) > max {
		return s[:max] + "..."
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

	filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
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
	})
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
		return sdkproton.Rule{}, err
	}
	return sdkproton.Rule{
		ID: "expression", Name: "Expression",
		Severity: "info", Requests: []*file.Request{req},
	}, nil
}

func normalizeShortFlags(args []string) []string {
	aliases := map[string]string{
		"-etags": "--exclude-tags",
		"-eid":   "--exclude-id",
		"-es":    "--exclude-severity",
		"-tl":    "--template-list",
	}
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if replacement, ok := aliases[arg]; ok {
			out = append(out, replacement)
		} else {
			out = append(out, arg)
		}
	}
	return out
}
