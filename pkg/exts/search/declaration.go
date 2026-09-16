package search

import (
	"context"
	"fmt"
	"strings"
	"time"

	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/resource"
	types "github.com/chainreactors/cyber/core/types"
	searchtools "github.com/chainreactors/cyber/tools/search"
)

const connectionTimeout = 20 * time.Second

func Declare(resources *resource.Registry) error {
	_, err := resource.Add[cfg.Connection](resources, cfg.Connection{Section: "search", Test: testConnection})
	return err
}

func testConnection(ctx context.Context, in, stored *types.DistributeConfig) []*types.ConnectionCheck {
	keys := fallbackString(in.GetSearch().GetTavilyKeys(), stored.GetSearch().GetTavilyKeys())
	return []*types.ConnectionCheck{connectionCheck("tavily", func() (string, error) {
		key := firstCSV(keys)
		if key == "" {
			return "", fmt.Errorf("no tavily api key configured")
		}
		probeCtx, cancel := context.WithTimeout(ctx, connectionTimeout)
		defer cancel()
		return searchtools.ProbeTavily(probeCtx, key, "")
	})}
}

func connectionCheck(name string, run func() (string, error)) *types.ConnectionCheck {
	started := time.Now()
	detail, err := run()
	result := &types.ConnectionCheck{Name: name, LatencyMs: time.Since(started).Milliseconds()}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Ok = true
	result.Detail = detail
	return result
}

func fallbackString(in, stored string) string {
	if strings.TrimSpace(in) != "" {
		return in
	}
	return stored
}

func firstCSV(value string) string {
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			return item
		}
	}
	return ""
}
