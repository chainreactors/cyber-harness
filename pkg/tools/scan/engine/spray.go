package engine

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/parsers"
	sdkkit "github.com/chainreactors/sdk/pkg"
	"github.com/chainreactors/sdk/spray"
	spraycore "github.com/chainreactors/spray/core"
)

type SprayCheckOptions struct {
	URLs          []string
	Host          string
	Dictionaries  []string
	Rules         []string
	Word          string
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
	Debug         bool
	OnStats       func(sdkkit.Stats)
}

func SprayCheckStream(ctx context.Context, eng *spray.SprayEngine, opts SprayCheckOptions) (<-chan *parsers.SprayResult, error) {
	if eng == nil {
		return nil, fmt.Errorf("spray engine is not available")
	}
	if opts.Debug {
		telemetry.EnableLogsDebug()
	}
	sprayCtx := spray.NewContext().
		WithContext(ctx).
		SetOption(buildSprayOption(opts)).
		SetStatsHandler(opts.OnStats)
	resultCh, err := eng.Execute(sprayCtx, spray.NewCheckTask(opts.URLs))
	if err != nil {
		return nil, err
	}

	out := make(chan *parsers.SprayResult)
	go func() {
		defer close(out)
		for result := range resultCh {
			if result == nil || !result.Success() {
				continue
			}
			sprayResult, ok := result.Data().(*parsers.SprayResult)
			if !ok || sprayResult == nil {
				continue
			}
			select {
			case out <- sprayResult:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func buildSprayOption(opts SprayCheckOptions) *spraycore.Option {
	sprayOpt := spray.NewDefaultOption()
	coreOpt := sprayOpt.Option
	coreOpt.Threads = opts.Threads
	coreOpt.Timeout = opts.Timeout
	coreOpt.Host = opts.Host
	coreOpt.Dictionaries = append([]string(nil), opts.Dictionaries...)
	coreOpt.Rules = append([]string(nil), opts.Rules...)
	coreOpt.Word = opts.Word
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
	return coreOpt
}
