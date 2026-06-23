package found

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

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/neutron/operators"
	"github.com/chainreactors/neutron/protocols"
	"github.com/chainreactors/proton/proton/file"
	goflags "github.com/jessevdk/go-flags"
)

type Command struct {
	workDir   string
	stdinFile string
}

func New() *Command { return &Command{} }

func (c *Command) SetWorkDir(dir string)    { c.workDir = dir }
func (c *Command) SetStdinFile(path string) { c.stdinFile = path }
func (c *Command) Name() string             { return "found" }

func (c *Command) Usage() string {
	return `found - sensitive information scanner
Usage: found -i <path> [options]
       <command> | found [options]
Input:
  -i, --input       Target file or directory to scan
  -e, --expression  Regex pattern to search directly (can specify multiple)
Options:
      --severity    Filter by severity: critical,high,medium,low,info
  -j, --json        JSON output
      --bin         Include binary files (default: text-only)
Pipe usage:
  curl -s http://target/api | found
  cat config.yaml | found -e "password|secret|token"
  spray -u http://target | found
Examples:
  found -i /path/to/project
  found -i . --severity critical,high
  found -i . -e "AKIA[0-9A-Z]{16}" -e "password\s*[:=]"`
}

type flags struct {
	Input       string   `short:"i" long:"input" description:"target file or directory"`
	Expressions []string `short:"e" long:"expression" description:"regex pattern to search"`
	Severity    string   `long:"severity" description:"severity filter"`
	JSON        bool     `short:"j" long:"json" description:"JSON output"`
	Bin         bool     `long:"bin" description:"include binary files"`
}

func (c *Command) Execute(ctx context.Context, args []string) error {
	var f flags
	parser := goflags.NewParser(&f, goflags.Default&^goflags.PrintErrors)
	remaining, err := parser.ParseArgs(args)
	if err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			fmt.Fprint(commands.Output, c.Usage()+"\n")
			return nil
		}
		return fmt.Errorf("found: %w", err)
	}

	input := c.resolveInput(f.Input, remaining)

	if input == "" && c.stdinFile != "" {
		input = c.stdinFile
		defer func() {
			os.Remove(c.stdinFile)
			c.stdinFile = ""
		}()
	}

	if input == "" && len(f.Expressions) == 0 {
		return fmt.Errorf("found: target required (-i <path>, -e <regex>, or pipe: <cmd> | found)")
	}

	execOpts := &protocols.ExecuterOptions{
		Options: &protocols.Options{TextOnly: !f.Bin},
	}

	rules := builtinRules(execOpts)

	if len(f.Expressions) > 0 {
		rule, exprErr := buildExpressionRule(f.Expressions, execOpts)
		if exprErr != nil {
			return fmt.Errorf("found: %v", exprErr)
		}
		rules = append(rules, rule)
	}

	if len(rules) == 0 {
		return fmt.Errorf("found: no rules loaded")
	}

	scanner := file.NewScanner(rules, execOpts)

	sevFilter := parseSeverity(f.Severity)
	seen := make(map[string]bool)
	var findingCount int64

	callback := func(uf file.Finding) {
		if len(sevFilter) > 0 {
			if _, ok := sevFilter[uf.Severity]; !ok {
				return
			}
		}
		key := uf.TemplateID + "|" + uf.FilePath
		if seen[key] {
			return
		}
		seen[key] = true
		atomic.AddInt64(&findingCount, 1)
		writeFinding(commands.Output, uf, f.JSON, input)
	}

	if input != "" {
		info, statErr := os.Stat(input)
		if statErr != nil {
			return fmt.Errorf("found: %v", statErr)
		}
		if info.IsDir() {
			walkAndScan(scanner, input, callback)
		} else {
			scanSingleFile(scanner, input, callback)
		}
	}

	count := atomic.LoadInt64(&findingCount)
	fileCount := atomic.LoadInt64(&scanner.Stats.Files)
	ruleCount := scanner.Stats.Rules
	if count > 0 {
		fmt.Fprintf(commands.Output, "\n[found] %d findings | %d rules | %d files\n", count, ruleCount, fileCount)
	} else {
		fmt.Fprintf(commands.Output, "[found] no findings | %d rules | %d files\n", ruleCount, fileCount)
	}
	return nil
}

func (c *Command) resolveInput(input string, remaining []string) string {
	if input == "" && len(remaining) > 0 {
		input = remaining[0]
	}
	if input != "" && !filepath.IsAbs(input) && c.workDir != "" {
		input = filepath.Join(c.workDir, input)
	}
	return input
}

// --- output ---

func writeFinding(w *commands.OutputWriter, f file.Finding, jsonMode bool, baseDir string) {
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

func walkAndScan(scanner *file.Scanner, target string, callback func(file.Finding)) {
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
		if err != nil {
			return nil
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

func parseSeverity(s string) map[string]bool {
	if s == "" {
		return nil
	}
	m := make(map[string]bool)
	for _, sev := range strings.Split(s, ",") {
		m[strings.TrimSpace(strings.ToLower(sev))] = true
	}
	return m
}

func buildExpressionRule(expressions []string, execOpts *protocols.ExecuterOptions) (file.Rule, error) {
	req := &file.Request{Extensions: []string{"all"}}
	var extractors []*operators.Extractor
	for _, expr := range expressions {
		extractors = append(extractors, &operators.Extractor{
			Type:  "regex",
			Regex: []string{expr},
		})
	}
	req.Extractors = extractors
	if err := req.Compile(execOpts); err != nil {
		return file.Rule{}, err
	}
	return file.Rule{
		ID: "expression", Name: "Expression",
		Severity: "info", Requests: []*file.Request{req},
	}, nil
}
