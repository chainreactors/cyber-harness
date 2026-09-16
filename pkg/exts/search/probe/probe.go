package probe

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/core/resource"
	registry "github.com/chainreactors/cyber/pkg/probe"
	types "github.com/chainreactors/cyber/pkg/types"
	"github.com/chainreactors/cyber/tools/search"
	"strings"
	"time"
)

const connProbeTimeout = 20 * time.Second

func Declare(resources *resource.Registry) error {
	_, err := resource.Add[registry.Definition](resources, registry.Definition{Section: "search", Check: Check})
	return err
}

func Check(ctx context.Context, in, stored *types.DistributeConfig) []*types.ConnectionCheck {
	keys := fallbackStr(in.GetSearch().GetTavilyKeys(), stored.GetSearch().GetTavilyKeys())
	return []*types.ConnectionCheck{runCheck("tavily", func() (string, error) {
		first := firstCSV(keys)
		if first == "" {
			return "", fmt.Errorf("no tavily api key configured")
		}
		probeCtx, cancel := context.WithTimeout(ctx, connProbeTimeout)
		defer cancel()
		return search.ProbeTavily(probeCtx, first, "")
	})}
}

func runCheck(name string, fn func() (string, error)) *types.ConnectionCheck {
	start := time.Now()
	detail, err := fn()
	c := &types.ConnectionCheck{Name: name, LatencyMs: time.Since(start).Milliseconds()}
	if err != nil {
		c.Error = err.Error()
		return c
	}
	c.Ok = true
	c.Detail = detail
	return c
}

// fallbackStr returns in when non-blank, otherwise the stored value.
func fallbackStr(in, stored string) string {
	if strings.TrimSpace(in) != "" {
		return in
	}
	return stored
}

func firstCSV(s string) string {
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			return p
		}
	}
	return ""
}
