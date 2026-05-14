package engine

import (
	"context"
	"fmt"

	"github.com/chainreactors/parsers"
	sdkzombie "github.com/chainreactors/sdk/zombie"
)

type ZombieWeakpassOptions struct {
	Targets   []sdkzombie.Target
	Users     []string
	Passwords []string
	Threads   int
	Timeout   int
	Top       int
}

func ZombieWeakpassStream(ctx context.Context, eng *sdkzombie.Engine, opts ZombieWeakpassOptions) (<-chan *parsers.ZombieResult, error) {
	if eng == nil {
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
	return eng.WeakpassStream(zctx, task)
}
