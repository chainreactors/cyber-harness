package engine

import (
	"context"
	"fmt"

	"github.com/chainreactors/cyber/core/telemetry"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
	sdkzombie "github.com/chainreactors/sdk/zombie"
	"github.com/chainreactors/utils/parsers"
)

type ZombieWeakpassOptions struct {
	Targets   []sdkzombie.Target
	Users     []string
	Passwords []string
	Threads   int
	Timeout   int
	Top       int
	Proxy     string
	Debug     bool
	OnStats   func(sdktypes.Stats)
}

func ZombieWeakpassStream(ctx context.Context, eng *sdkzombie.Engine, opts ZombieWeakpassOptions) (<-chan *parsers.ZombieResult, error) {
	if eng == nil {
		return nil, fmt.Errorf("zombie engine is not available")
	}

	if opts.Debug {
		telemetry.EnableLogsDebug()
	}
	zctx := sdkzombie.NewContext().
		WithContext(ctx).
		WithThreads(opts.Threads).
		WithTimeout(opts.Timeout).
		WithTop(opts.Top).
		WithStatsHandler(opts.OnStats)
	if opts.Proxy != "" {
		zctx = zctx.WithProxy(opts.Proxy)
	}

	task := sdkzombie.NewBruteTask(opts.Targets)
	task.Users = opts.Users
	task.Passwords = opts.Passwords
	resultCh, err := eng.Execute(zctx, task)
	if err != nil {
		return nil, err
	}

	return forwardResults(ctx, resultCh, func(result sdktypes.Result) (*parsers.ZombieResult, bool) {
		if result == nil || !result.Success() {
			return nil, false
		}
		value, ok := result.Data().(*parsers.ZombieResult)
		return value, ok && value != nil
	}, nil), nil
}
