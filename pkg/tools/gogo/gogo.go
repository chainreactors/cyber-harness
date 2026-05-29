package gogo

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/toolargs"
	gogocore "github.com/chainreactors/gogo/v2/core"
	gogopkg "github.com/chainreactors/gogo/v2/pkg"
	"github.com/chainreactors/parsers"
	"github.com/chainreactors/proxyclient"
	"github.com/chainreactors/sdk/gogo"
)

type Command struct {
	engine  *gogo.GogoEngine
	logger  telemetry.Logger
	proxy   string
	workDir string
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func New(engine *gogo.GogoEngine) *Command {
	return &Command{engine: engine, logger: telemetry.NopLogger()}
}

func (c *Command) WithLogger(logger telemetry.Logger) *Command {
	if logger != nil {
		c.logger = logger
	}
	return c
}

func (c *Command) SetWorkDir(dir string) { c.workDir = dir }

func (c *Command) WithProxy(proxy string) *Command {
	c.proxy = proxy
	return c
}

func (c *Command) SetProxy(proxy string) { c.proxy = proxy }

func (c *Command) Name() string { return "gogo" }

func (c *Command) Usage() string {
	return gogocore.Help()
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	args = c.normalizeArgs(args)
	args = c.injectProxy(args)

	if toolargs.BoolFlagEnabled(args, "--debug") {
		restoreDebug := telemetry.ActivateDebug(c.logger)
		defer restoreDebug()
		c.logger.Debugf("gogo debug enabled")
	}
	if c.engine != nil {
		c.engine.InstallResourceProvider()
	}

	// Prefer the SDK engine path: it runs the scan in a goroutine with
	// proper context cancellation at every IP×port dispatch.  The old
	// gogocore.RunWithArgs CLI path calls runner.Run() synchronously and
	// never checks ctx.Done() once scanning starts, so a large port sweep
	// through a slow SOCKS proxy blocks the agent indefinitely.
	if c.engine != nil {
		if opts, ok, err := parseSDKScanArgs(args); err != nil {
			return "", err
		} else if ok {
			return c.executeViaSDK(ctx, opts)
		}
		c.logger.Debugf("gogo: args contain unsupported flags, falling back to RunWithArgs wrapper")
	}

	// Fallback for SDK-uncovered invocations (workflow files, JSON input,
	// print/help modes) or when no engine is available. RunWithArgs is still
	// isolated in a goroutine because upstream only checks ctx before Run().
	return c.executeViaRunWithArgs(ctx, args)
}

// sdkScanArgs holds the parsed subset of gogo CLI flags that map directly
// to the SDK engine's ScanTask / Context API.
type sdkScanArgs struct {
	target       string
	ports        string
	proxies      []string
	threads      int
	delay        int
	httpsDelay   int
	versionLevel int
	exploit      string
	mod          string
	ping         bool
	debug        bool
}

// parseSDKScanArgs extracts structured parameters from the normalised arg
// slice. It returns false if the args contain features the SDK path doesn't
// cover yet (workflow files, JSON input, formatter modes, no-scan, etc.).
func parseSDKScanArgs(args []string) (*sdkScanArgs, bool, error) {
	// Reject immediately if any unsupported flag is present.
	for _, a := range args {
		key := a
		if strings.HasPrefix(a, "--") {
			key, _, _ = strings.Cut(a, "=")
		}
		switch {
		case key == "-j", key == "--json", key == "-J":
			return nil, false, nil
		case key == "-w", key == "--workflow", key == "-W":
			return nil, false, nil
		case key == "-F", key == "--format":
			return nil, false, nil
		case key == "-n", key == "--no":
			return nil, false, nil
		}
	}

	opts := &sdkScanArgs{
		ports:      "top1",
		delay:      2,
		httpsDelay: 2,
		exploit:    "none",
	}
	listFile := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, val, hasEq := "", "", false
		if strings.HasPrefix(arg, "--") {
			key, val, hasEq = strings.Cut(arg, "=")
		}

		nextVal := func() (string, bool) {
			if hasEq {
				return val, true
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				return args[i], true
			}
			return "", false
		}

		switch {
		case arg == "-i" || key == "--ip":
			if v, ok := nextVal(); ok {
				opts.target = v
			}
		case arg == "-l" || arg == "-L" || key == "--list":
			if v, ok := nextVal(); ok {
				listFile = v
			}
		case arg == "-p" || key == "--port":
			if v, ok := nextVal(); ok {
				opts.ports = v
			}
		case arg == "--proxy":
			if v, ok := nextVal(); ok {
				opts.proxies = append(opts.proxies, v)
			}
		case arg == "-t" || key == "--thread":
			if v, ok := nextVal(); ok {
				fmt.Sscanf(v, "%d", &opts.threads)
			}
		case arg == "-d" || key == "--timeout":
			if v, ok := nextVal(); ok {
				fmt.Sscanf(v, "%d", &opts.delay)
			}
		case arg == "-D" || key == "--ssl-timeout":
			if v, ok := nextVal(); ok {
				fmt.Sscanf(v, "%d", &opts.httpsDelay)
			}
		case arg == "-v" || arg == "--verbose":
			opts.versionLevel++
		case arg == "-vv":
			opts.versionLevel = 2
		case arg == "-e" || arg == "--exploit":
			opts.exploit = "auto"
		case arg == "-E" || key == "--exploit-name":
			if v, ok := nextVal(); ok {
				opts.exploit = v
			}
		case arg == "-m" || key == "--mod":
			if v, ok := nextVal(); ok {
				opts.mod = v
			}
		case arg == "--ping" || key == "--ping":
			opts.ping = !hasEq || !isFalseFlagValue(val)
		case arg == "--debug":
			opts.debug = true
		// Skip output formatting flags — they don't affect scanning, and we
		// format results ourselves via GOGOResult.FullOutput().
		case arg == "-o", arg == "--output", arg == "-O", arg == "--file-output",
			arg == "-f", arg == "--file", arg == "--path",
			arg == "-q", arg == "--quiet",
			arg == "-s", arg == "--spray", arg == "--no-spray",
			arg == "-C", arg == "--compress", arg == "--tee",
			arg == "--af", arg == "--hf",
			arg == "--output-delimiter",
			arg == "--filter", arg == "--output-filter", arg == "--scan-filter",
			arg == "--exclude", arg == "--exclude-file",
			arg == "--no-guess", arg == "--opsec",
			arg == "--extract", arg == "--payload", arg == "--attack-type",
			arg == "--ef", arg == "--ff",
			arg == "-k", arg == "--key",
			arg == "--version", arg == "-P", arg == "--print",
			arg == "--plugin-debug", arg == "--port-config",
			arg == "--sp", arg == "--ipp":
			// consume the value if this is a value-bearing flag
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
		}
	}

	if opts.target == "" && listFile != "" {
		targets, err := readGogoListFile(listFile)
		if err != nil {
			return nil, false, fmt.Errorf("gogo: read list file: %w", err)
		}
		opts.target = targets
	}
	if opts.target == "" {
		return nil, false, nil
	}
	return opts, true, nil
}

// executeViaSDK runs the scan through the SDK engine, which spawns a
// goroutine that checks ctx.Done() at every IP and every ants pool dispatch.
func (c *Command) executeViaSDK(ctx context.Context, opts *sdkScanArgs) (string, error) {
	if err := c.engine.Init(); err != nil {
		return "", fmt.Errorf("gogo: init: %w", err)
	}

	runOpt := gogopkg.DefaultRunnerOption
	optCopy := *runOpt
	optCopy.Delay = opts.delay
	optCopy.HttpsDelay = opts.httpsDelay
	optCopy.VersionLevel = opts.versionLevel
	optCopy.Exploit = opts.exploit
	optCopy.Debug = opts.debug

	proxyURLs := opts.proxies
	if len(proxyURLs) == 0 && c.proxy != "" {
		proxyURLs = []string{c.proxy}
	}
	if len(proxyURLs) > 0 {
		if err := applyProxyToRunnerOption(&optCopy, proxyURLs); err != nil {
			c.logger.Debugf("gogo: proxy setup failed: %v", err)
		}
	}

	gogoCtx := gogo.NewContext().
		WithContext(ctx).
		SetOption(&optCopy)
	if opts.threads > 0 {
		gogoCtx = gogoCtx.SetThreads(opts.threads)
	}

	var task interface {
		Type() string
		Validate() error
	} = gogo.NewScanTask(opts.target, opts.ports)
	if opts.mod != "" || opts.ping {
		task = gogo.NewWorkflowTask(&gogopkg.Workflow{
			IP:    opts.target,
			Ports: opts.ports,
			Mod:   opts.mod,
			Ping:  opts.ping,
		})
	}
	resultCh, err := c.engine.Execute(gogoCtx, task)
	if err != nil {
		return "", fmt.Errorf("gogo: %w", err)
	}

	var buf bytes.Buffer
	for result := range resultCh {
		if !result.Success() {
			continue
		}
		if gogoResult, ok := result.Data().(*parsers.GOGOResult); ok && gogoResult != nil {
			buf.WriteString(gogoResult.FullOutput())
		}
	}
	return buf.String(), nil
}

// executeViaRunWithArgs is the fallback path for invocations the SDK engine API
// doesn't cover yet (workflows, JSON input, print/help modes). It runs
// RunWithArgs in a goroutine so context cancellation unblocks the caller. If
// the fallback is a real scan, the upstream runner keeps going until it exits.
func (c *Command) executeViaRunWithArgs(ctx context.Context, args []string) (string, error) {
	var buf lockedBuffer
	done := make(chan error, 1)

	go func() {
		done <- gogocore.RunWithArgs(ctx, args, gogocore.RunOptions{
			Output: &buf,
			BeforeInit: func() error {
				if c.engine != nil {
					c.engine.InstallResourceProvider()
				}
				return nil
			},
			AfterInit: func() error {
				if c.engine == nil {
					return nil
				}
				return c.engine.Init()
			},
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			return buf.String(), fmt.Errorf("gogo: %w", err)
		}
		return buf.String(), nil
	case <-ctx.Done():
		// Unblock the caller.  The RunWithArgs goroutine continues in the
		// background — this is a known leak, but the alternative is blocking
		// the entire agent for hours.
		return buf.String(), fmt.Errorf("gogo: %w (scan orphaned)", ctx.Err())
	}
}

func readGogoListFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var targets []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		targets = append(targets, line)
	}
	if len(targets) == 0 {
		return "", fmt.Errorf("%s contains no targets", path)
	}
	return strings.Join(targets, ","), nil
}

func isFalseFlagValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}

func applyProxyToRunnerOption(opt *gogopkg.RunnerOption, proxyURLs []string) error {
	var proxies []*url.URL
	for _, u := range proxyURLs {
		uri, err := url.Parse(u)
		if err != nil {
			return fmt.Errorf("parse proxy %q: %w", u, err)
		}
		proxies = append(proxies, uri)
	}
	dialer, err := proxyclient.NewClientChain(proxies)
	if err != nil {
		return fmt.Errorf("create proxy chain: %w", err)
	}
	dialCtx := dialer.DialContext
	opt.ProxyDialContext = dialCtx
	opt.ProxyDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return dialCtx(ctx, network, address)
	}
	return nil
}

// TestInjectProxy is exported for cross-package testing.
func (c *Command) TestInjectProxy(args []string) []string {
	return c.injectProxy(args)
}

func (c *Command) injectProxy(args []string) []string {
	if c.proxy == "" {
		return args
	}
	if toolargs.HasFlag(args, "--proxy") {
		return args
	}
	return append(args, "--proxy", c.proxy)
}

// normalizeArgs adapts common agent-generated gogo arguments before handing
// them to the upstream parser. gogo's -j/--json is an input file, while agents
// often use it as a boolean JSON-output flag; treat valueless -j as -o jl.
func (c *Command) normalizeArgs(args []string) []string {
	out := make([]string, 0, len(args)+2)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if isGogoValuelessJSONFlag(arg, args, i) {
			out = append(out, "-o", "jl")
			continue
		}
		if key, value, ok := splitLongFlagValue(arg); ok {
			switch {
			case isGogoFileFlag(key):
				out = append(out, key+"="+c.resolvePathArg(value))
			case isGogoOutputFormatFlag(key):
				out = append(out, key+"="+normalizeOutputFormat(value))
			default:
				out = append(out, arg)
			}
			continue
		}
		if isGogoFileFlag(arg) {
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, c.resolvePathArg(args[i]))
			}
			continue
		}
		if isGogoOutputFormatFlag(arg) {
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, normalizeOutputFormat(args[i]))
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

func (c *Command) resolvePathArg(value string) string {
	if c.workDir == "" || value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "-") {
		return value
	}
	return filepath.Join(c.workDir, value)
}

func isGogoValuelessJSONFlag(arg string, args []string, index int) bool {
	if arg != "-j" && arg != "--json" {
		return false
	}
	return index+1 >= len(args) || strings.HasPrefix(args[index+1], "-")
}

func splitLongFlagValue(arg string) (string, string, bool) {
	if !strings.HasPrefix(arg, "--") {
		return "", "", false
	}
	key, value, ok := strings.Cut(arg, "=")
	return key, value, ok
}

func isGogoFileFlag(flag string) bool {
	switch flag {
	case "-f", "--file",
		"--path",
		"-l", "-L", "--list",
		"-j", "--json",
		"-F", "--format",
		"--exclude-file",
		"--port-config",
		"--ef", "--ff":
		return true
	default:
		return false
	}
}

func isGogoOutputFormatFlag(flag string) bool {
	switch flag {
	case "-o", "--output", "-O", "--file-output":
		return true
	default:
		return false
	}
}

func normalizeOutputFormat(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "jsonl":
		return "jl"
	default:
		return value
	}
}
