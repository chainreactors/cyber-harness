package engine

import (
	"context"
	"fmt"
	"os"

	gogopkg "github.com/chainreactors/gogo/v2/pkg"
	"github.com/chainreactors/sdk/gogo"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
	"github.com/chainreactors/utils/parsers"
)

const GogoTempLogFile = ".sock.lock"

type GogoScanOptions struct {
	Target       string
	Ports        string
	Threads      int
	Timeout      int
	VersionLevel int
	Exploit      string
	Proxy        string
	Debug        bool
	OnStats      func(sdktypes.Stats)
}

func GogoScanStream(ctx context.Context, eng *gogo.Engine, opts GogoScanOptions) (<-chan *parsers.GOGOResult, error) {
	if eng == nil {
		return nil, fmt.Errorf("gogo engine is not available")
	}

	CleanupGogoTempFiles()
	runOpt := buildGogoRunnerOption(opts)
	gogoCtx := gogo.NewContext().
		WithContext(ctx).
		WithThreads(opts.Threads).
		WithOption(runOpt).
		WithStatsHandler(opts.OnStats)
	if opts.Proxy != "" {
		gogoCtx = gogoCtx.WithProxy(opts.Proxy)
	}
	resultCh, err := eng.Execute(gogoCtx, gogo.NewScanTask(opts.Target, opts.Ports))
	if err != nil {
		CleanupGogoTempFiles()
		return nil, err
	}

	return forwardResults(ctx, resultCh, func(result sdktypes.Result) (*parsers.GOGOResult, bool) {
		if result == nil || !result.Success() {
			return nil, false
		}
		value, ok := result.Data().(*parsers.GOGOResult)
		return value, ok && value != nil
	}, CleanupGogoTempFiles), nil
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
