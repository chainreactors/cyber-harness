package engine

import (
	"context"
	"fmt"
	"os"

	gogopkg "github.com/chainreactors/gogo/v2/pkg"
	"github.com/chainreactors/parsers"
	"github.com/chainreactors/sdk/gogo"
	sdkkit "github.com/chainreactors/sdk/pkg"
)

const GogoTempLogFile = ".sock.lock"

type GogoScanOptions struct {
	Target       string
	Ports        string
	Threads      int
	Timeout      int
	VersionLevel int
	Exploit      string
	Debug        bool
	OnStats      func(sdkkit.Stats)
}

func GogoScanStream(ctx context.Context, eng *gogo.GogoEngine, opts GogoScanOptions) (<-chan *parsers.GOGOResult, error) {
	if eng == nil {
		return nil, fmt.Errorf("gogo engine is not available")
	}
	CleanupGogoTempFiles()
	runOpt := buildGogoRunnerOption(opts)
	gogoCtx := gogo.NewContext().
		WithContext(ctx).
		SetThreads(opts.Threads).
		SetOption(runOpt).
		SetStatsHandler(opts.OnStats)
	resultCh, err := eng.Execute(gogoCtx, gogo.NewScanTask(opts.Target, opts.Ports))
	if err != nil {
		CleanupGogoTempFiles()
		return nil, err
	}

	out := make(chan *parsers.GOGOResult)
	go func() {
		defer CleanupGogoTempFiles()
		defer close(out)
		for result := range resultCh {
			if result == nil || !result.Success() {
				continue
			}
			gogoResult, ok := result.Data().(*parsers.GOGOResult)
			if !ok || gogoResult == nil {
				continue
			}
			select {
			case out <- gogoResult:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func buildGogoRunnerOption(opts GogoScanOptions) *gogopkg.RunnerOption {
	runOpt := *gogopkg.DefaultRunnerOption
	if opts.Timeout > 0 {
		runOpt.Delay = opts.Timeout
		runOpt.HttpsDelay = opts.Timeout
	}
	if opts.VersionLevel > 0 {
		runOpt.VersionLevel = opts.VersionLevel
	}
	if opts.Exploit != "" {
		runOpt.Exploit = opts.Exploit
	}
	runOpt.Debug = opts.Debug
	return &runOpt
}

func CleanupGogoTempFiles() {
	if err := os.Remove(GogoTempLogFile); err != nil && !os.IsNotExist(err) {
		return
	}
}
