package engine

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/parsers"
	sdkkit "github.com/chainreactors/sdk/pkg"
	sdkzombie "github.com/chainreactors/sdk/zombie"
)

type ZombieWeakpassOptions struct {
	Targets   []sdkzombie.Target
	Users     []string
	Passwords []string
	Threads   int
	Timeout   int
	Top       int
	Debug     bool
	OnStats   func(sdkkit.Stats)
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
		SetThreads(opts.Threads).
		SetTimeout(opts.Timeout).
		SetTop(opts.Top).
		SetStatsHandler(opts.OnStats)

	task := sdkzombie.NewWeakpassTask(opts.Targets)
	task.Users = opts.Users
	task.Passwords = opts.Passwords
	resultCh, err := eng.Execute(zctx, task)
	if err != nil {
		return nil, err
	}

	out := make(chan *parsers.ZombieResult)
	go func() {
		defer close(out)
		for result := range resultCh {
			if result == nil || !result.Success() {
				continue
			}
			zombieResult, ok := result.Data().(*parsers.ZombieResult)
			if !ok || zombieResult == nil {
				continue
			}
			select {
			case out <- zombieResult:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
