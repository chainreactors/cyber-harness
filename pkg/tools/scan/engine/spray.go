package engine

import (
	"context"
	"fmt"

	"github.com/chainreactors/parsers"
	"github.com/chainreactors/sdk/spray"
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
}

func SprayCheckStream(ctx context.Context, eng *spray.SprayEngine, opts SprayCheckOptions) (<-chan *parsers.SprayResult, error) {
	if eng == nil {
		return nil, fmt.Errorf("spray engine is not available")
	}
	sprayCtx := spray.NewContext().
		WithContext(ctx).
		SetThreads(opts.Threads).
		SetTimeout(opts.Timeout).
		SetHost(opts.Host).
		SetDictionaries(opts.Dictionaries).
		SetRules(opts.Rules).
		SetWord(opts.Word).
		SetDefaultDict(opts.DefaultDict).
		SetAdvance(opts.Advance).
		SetCrawlPlugin(opts.Crawl).
		SetFinger(opts.Finger).
		SetActivePlugin(opts.ActivePlugin).
		SetReconPlugin(opts.ReconPlugin).
		SetBakPlugin(opts.BakPlugin).
		SetFuzzuliPlugin(opts.FuzzuliPlugin).
		SetCommonPlugin(opts.CommonPlugin)
	if opts.CrawlDepth > 0 {
		sprayCtx.SetCrawlDepth(opts.CrawlDepth)
	}
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
