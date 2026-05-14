package scan

import (
	"context"
	"errors"
	"fmt"
	"os"

	gogopkg "github.com/chainreactors/gogo/v2/pkg"
	"github.com/chainreactors/neutron/templates"
	"github.com/chainreactors/parsers"
	"github.com/chainreactors/sdk/gogo"
	"github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/association"
	"github.com/chainreactors/sdk/spray"
	sdkzombie "github.com/chainreactors/sdk/zombie"
)

var errNoNeutronTemplates = errors.New("no neutron templates selected")

const gogoTempLogFile = ".sock.lock"

type gogoScanOptions struct {
	Target       string
	Ports        string
	Threads      int
	Timeout      int
	VersionLevel int
}

type sprayCheckOptions struct {
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

type zombieWeakpassOptions struct {
	Targets   []sdkzombie.Target
	Users     []string
	Passwords []string
	Threads   int
	Timeout   int
	Top       int
}

type neutronExecuteOptions struct {
	Target       string
	Fingers      []string
	MaxPerFinger int
	Broad        bool
}

func gogoScanStream(ctx context.Context, engine *gogo.GogoEngine, opts gogoScanOptions) (<-chan *parsers.GOGOResult, error) {
	if engine == nil {
		return nil, fmt.Errorf("gogo engine is not available")
	}
	cleanupGogoTempFiles()
	runOpt := *gogopkg.DefaultRunnerOption
	if opts.Timeout > 0 {
		runOpt.Delay = opts.Timeout
		runOpt.HttpsDelay = opts.Timeout
	}
	if opts.VersionLevel > 0 {
		runOpt.VersionLevel = opts.VersionLevel
	}
	gogoCtx := gogo.NewContext().
		WithContext(ctx).
		SetThreads(opts.Threads).
		SetOption(&runOpt)
	resultCh, err := engine.ScanStream(gogoCtx, opts.Target, opts.Ports)
	if err != nil {
		cleanupGogoTempFiles()
		return nil, err
	}

	out := make(chan *parsers.GOGOResult)
	go func() {
		defer cleanupGogoTempFiles()
		defer close(out)
		for result := range resultCh {
			select {
			case out <- result:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func cleanupGogoTempFiles() {
	if err := os.Remove(gogoTempLogFile); err != nil && !os.IsNotExist(err) {
		return
	}
}

func sprayCheckStream(ctx context.Context, engine *spray.SprayEngine, opts sprayCheckOptions) (<-chan *parsers.SprayResult, error) {
	if engine == nil {
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
	resultCh, err := engine.Execute(sprayCtx, spray.NewCheckTask(opts.URLs))
	if err != nil {
		return nil, err
	}

	out := make(chan *parsers.SprayResult)
	go func() {
		defer close(out)
		for result := range resultCh {
			if result == nil {
				continue
			}
			if !result.Success() {
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

func zombieWeakpassStream(ctx context.Context, engine *sdkzombie.Engine, opts zombieWeakpassOptions) (<-chan *parsers.ZombieResult, error) {
	if engine == nil {
		return nil, fmt.Errorf("zombie engine is not available")
	}
	zctx := sdkzombie.NewContext().
		WithContext(ctx).
		SetThreads(opts.Threads).
		SetTimeout(opts.Timeout).
		SetTop(opts.Top)

	task := sdkzombie.NewWeakpassTask(opts.Targets)
	task.Users = opts.Users
	task.Passwords = opts.Passwords
	return engine.WeakpassStream(zctx, task)
}

func neutronExecuteStream(ctx context.Context, engine *neutron.Engine, index *association.FingerPOCIndex, opts neutronExecuteOptions) (<-chan *neutron.ExecuteResult, error) {
	if engine == nil {
		return nil, fmt.Errorf("neutron engine is not available")
	}
	task := neutron.NewExecuteTask(opts.Target)
	selected, filtered := selectNeutronTemplates(engine, index, opts)
	if filtered {
		if len(selected) == 0 {
			return nil, errNoNeutronTemplates
		}
		task.Templates = selected
	}

	resultCh, err := engine.Execute(neutron.NewContext().WithContext(ctx), task)
	if err != nil {
		return nil, err
	}

	out := make(chan *neutron.ExecuteResult)
	go func() {
		defer close(out)
		for result := range resultCh {
			execResult, ok := result.(*neutron.ExecuteResult)
			if !ok {
				continue
			}
			select {
			case out <- execResult:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func selectNeutronTemplates(engine *neutron.Engine, index *association.FingerPOCIndex, opts neutronExecuteOptions) ([]*templates.Template, bool) {
	if len(opts.Fingers) == 0 {
		if opts.Broad {
			return nil, false
		}
		return nil, true
	}
	if engine == nil {
		return nil, true
	}

	allowedByFinger := make(map[string]struct{})
	if index == nil {
		return nil, true
	}
	for _, finger := range opts.Fingers {
		ids := index.GetPOCsByFinger(finger)
		if opts.MaxPerFinger > 0 && len(ids) > opts.MaxPerFinger {
			ids = ids[:opts.MaxPerFinger]
		}
		for _, id := range ids {
			allowedByFinger[id] = struct{}{}
		}
	}
	if len(allowedByFinger) == 0 {
		return nil, true
	}

	selected := make([]*templates.Template, 0)
	for _, tmpl := range engine.Get() {
		if tmpl == nil {
			continue
		}
		if _, ok := allowedByFinger[tmpl.Id]; !ok {
			continue
		}
		selected = append(selected, tmpl)
	}
	return selected, true
}
