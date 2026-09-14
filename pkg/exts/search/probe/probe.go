package probe

import (
	"context"
	"fmt"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/aiscan/tools/search"
	"strings"
	"time"
)

const connProbeTimeout = 20 * time.Second

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
