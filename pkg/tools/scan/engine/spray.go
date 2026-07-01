package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/utils/parsers"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
	"github.com/chainreactors/sdk/spray"
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
	OnStats       func(sdktypes.Stats)
}

func SprayCheckStream(ctx context.Context, eng *spray.Engine, opts SprayCheckOptions) (<-chan *parsers.SprayResult, error) {
	if eng == nil {
		return nil, fmt.Errorf("spray engine is not available")
	}
	if opts.Debug {
		telemetry.EnableLogsDebug()
	}
	runCtx, cancel := sprayInvocationContext(ctx, opts)
	sprayCtx := spray.NewContext().
		WithContext(runCtx).
		SetOption(buildSprayOption(opts)).
		SetStatsHandler(opts.OnStats)

	var resultCh <-chan sdktypes.Result
	var err error
	if needsBruteMode(opts) {
		resultCh, err = eng.Execute(sprayCtx, spray.NewBruteTasks(opts.URLs, crawlSeedWordlist(opts)))
	} else {
		resultCh, err = eng.Execute(sprayCtx, spray.NewCheckTask(opts.URLs))
	}
	if err != nil {
		cancel()
		return nil, err
	}

	out := make(chan *parsers.SprayResult)
	go func() {
		defer telemetry.SDKGoRecover("spray")
		defer cancel()
		defer close(out)
		for {
			var result sdktypes.Result
			var ok bool
			select {
			case result, ok = <-resultCh:
				if !ok {
					return
				}
			case <-runCtx.Done():
				return
			}
			if result == nil || !result.Success() {
				continue
			}
			sprayResult, ok := result.Data().(*parsers.SprayResult)
			if !ok || sprayResult == nil {
				continue
			}
			select {
			case out <- sprayResult:
			case <-runCtx.Done():
				return
			}
		}
	}()
	return out, nil
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
	if opts.Debug {
		coreOpt.Quiet = false
	}
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

// crawlSeedWordlist returns a minimal seed wordlist so the brute runner's
// initial request triggers response-body URL extraction by the crawl plugin.
// When dictionaries/word/defaultDict are set, spray's runner will load them
// internally, but BruteTask.Validate still requires a non-empty wordlist.
func crawlSeedWordlist(opts SprayCheckOptions) []string {
	return []string{"/"}
}
