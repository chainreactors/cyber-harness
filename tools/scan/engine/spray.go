package engine

import (
	"context"
	"fmt"
	"time"

	sdktypes "github.com/chainreactors/sdk/pkg/types"
	"github.com/chainreactors/sdk/spray"
	"github.com/chainreactors/utils/parsers"
)

type SprayCheckOptions struct {
	URLs          []string
	Host          string
	Dictionaries  []string
	Rules         []string
	Word          string
	Scope         []string
	DefaultDict   bool
	Advance       bool
	Crawl         bool
	Finger        bool
	ActivePlugin  bool
	ReconPlugin   bool
	BakPlugin     bool
	FuzzuliPlugin bool
	CommonPlugin  bool
	CrawlDepth    int
	Threads       int
	Timeout       int
	MaxDuration   time.Duration
	Proxy         string
	Debug         bool
	Quiet         bool
	OnStats       func(sdktypes.Stats)
}

func SprayCheckStream(ctx context.Context, eng *spray.Engine, opts SprayCheckOptions) (<-chan *parsers.SprayResult, error) {
	if eng == nil {
		return nil, fmt.Errorf("spray engine is not available")
	}

	release, err := AcquireSpray(ctx)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	runCtx, cancel := sprayInvocationContext(ctx, opts)
	sprayCtx := spray.NewContext().
		WithContext(runCtx).
		WithOption(buildSprayOption(opts)).
		WithStatsHandler(opts.OnStats)

	var resultCh <-chan sdktypes.Result
	if needsBruteMode(opts) {
		// BruteTask.Validate requires a seed; spray loads requested dictionaries itself.
		resultCh, err = eng.Execute(sprayCtx, spray.NewBruteTasks(opts.URLs, []string{"/"}))
	} else {
		resultCh, err = eng.Execute(sprayCtx, spray.NewCheckTask(opts.URLs))
	}
	if err != nil {
		cancel()
		return nil, err
	}

	transferred = true
	return forwardResults(runCtx, resultCh, func(result sdktypes.Result) (*parsers.SprayResult, bool) {
		if result == nil || !result.Success() {
			return nil, false
		}
		value, ok := result.Data().(*parsers.SprayResult)
		return value, ok && value != nil
	}, func() { cancel(); release() }), nil
}

func sprayInvocationContext(parent context.Context, opts SprayCheckOptions) (context.Context, context.CancelFunc) {
	if opts.MaxDuration > 0 {
		return context.WithTimeout(parent, opts.MaxDuration)
	}
	if d := defaultSprayInvocationTimeout(opts); d > 0 {
		return context.WithTimeout(parent, d)
	}
	return context.WithCancel(parent)
}

func defaultSprayInvocationTimeout(opts SprayCheckOptions) time.Duration {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5
	}
	multiplier := 4
	if needsBruteMode(opts) || opts.CommonPlugin || opts.ActivePlugin || opts.BakPlugin || opts.FuzzuliPlugin || opts.ReconPlugin {
		multiplier = 12
	}
	seconds := timeout * multiplier
	if opts.CrawlDepth > 0 {
		seconds += opts.CrawlDepth * 10
	}
	if seconds < 30 {
		seconds = 30
	}
	if seconds > 120 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func buildSprayOption(opts SprayCheckOptions) *spray.Option {
	sprayOpt := spray.NewDefaultOption()
	coreOpt := sprayOpt.Option
	// The SDK configures its shared logger once when the engine is initialized.
	// JSON mode opts into quiet per-run output so progress banners cannot corrupt
	// the command's JSONL stdout contract; ordinary terminal mode keeps the
	// existing progress output.
	coreOpt.Quiet = opts.Quiet
	coreOpt.Threads = opts.Threads
	coreOpt.Timeout = opts.Timeout
	coreOpt.Host = opts.Host
	coreOpt.Dictionaries = append([]string(nil), opts.Dictionaries...)
	coreOpt.Rules = append([]string(nil), opts.Rules...)
	coreOpt.Word = opts.Word
	coreOpt.Scope = append([]string(nil), opts.Scope...)
	coreOpt.DefaultDict = opts.DefaultDict
	coreOpt.Advance = opts.Advance
	coreOpt.CrawlPlugin = opts.Crawl
	coreOpt.Finger = opts.Finger
	coreOpt.ActivePlugin = opts.ActivePlugin
	coreOpt.ReconPlugin = opts.ReconPlugin
	coreOpt.BakPlugin = opts.BakPlugin
	coreOpt.FuzzuliPlugin = opts.FuzzuliPlugin
	coreOpt.CommonPlugin = opts.CommonPlugin
	if opts.CrawlDepth > 0 {
		coreOpt.CrawlDepth = opts.CrawlDepth
	}
	coreOpt.Debug = opts.Debug
	if opts.Proxy != "" {
		coreOpt.Proxies = []string{opts.Proxy}
	}
	return sprayOpt
}

// needsBruteMode returns true when the requested options require the brute
// pool (which hosts the crawl plugin, dict/rule bruting, etc.) instead of
// the lightweight check-only path.
func needsBruteMode(opts SprayCheckOptions) bool {
	return opts.Crawl || opts.DefaultDict || len(opts.Dictionaries) > 0 || opts.Word != ""
}

// Spray's native parser and SDK runner both mutate upstream global options,
// logging and resource providers. Their whole invocations share this gate.
var sprayExecution = make(chan struct{}, 1)

// AcquireSpray admits one native or SDK invocation. Cancellation may stop
// waiting for admission; an admitted caller releases only after upstream drains.
func AcquireSpray(ctx context.Context) (func(), error) {
	select {
	case sprayExecution <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-sprayExecution
		return nil, err
	}
	return func() { <-sprayExecution }, nil
}
