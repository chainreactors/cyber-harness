package scanner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/cyber/core/resource"
	types "github.com/chainreactors/cyber/core/types"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/sdk/pkg/cyberhub"
)

const connectionTimeout = 20 * time.Second

func Declare(resources *resource.Registry) error {
	if _, err := resource.Add[cfg.Section](resources, CyberhubSection(), ScanSection()); err != nil {
		return err
	}
	if _, err := resource.Add[hostcli.Contribution](resources, contributeFlags); err != nil {
		return err
	}
	_, err := resource.Add[cfg.Connection](resources,
		cfg.Connection{Section: CyberhubConfigKey, Test: testCyberhubConnection},
	)
	return err
}

// contributeFlags mounts the scanner flag groups on the commands that run
// scanner-backed workloads.
func contributeFlags(registry *hostcli.Registry) error {
	return registry.Group("agent", CyberhubConfigKey, CyberhubFlagGroup())
}

func testCyberhubConnection(ctx context.Context, in, stored *types.DistributeConfig) []*types.ConnectionCheck {
	inHub, storedHub := connectionSection(in, CyberhubConfigKey), connectionSection(stored, CyberhubConfigKey)
	hubURL := fallbackString(connectionString(inHub, "url"), connectionString(storedHub, "url"))
	key := fallbackString(connectionString(inHub, "key"), connectionString(storedHub, "key"))
	return []*types.ConnectionCheck{connectionCheck("cyberhub", func() (string, error) {
		if strings.TrimSpace(hubURL) == "" {
			return "", fmt.Errorf("cyberhub url is empty")
		}
		probeCtx, cancel := context.WithTimeout(ctx, connectionTimeout)
		defer cancel()
		provider := cyberhub.NewProvider(hubURL, key).
			WithTimeout(connectionTimeout).
			WithFilter(&cyberhub.ExportFilter{Limit: 1})
		fingers, _, err := provider.Fingers(probeCtx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("reachable - %d fingerprint(s) sampled", len(fingers)), nil
	})}
}

func connectionSection(config *types.DistributeConfig, key string) map[string]any {
	if config == nil {
		return nil
	}
	return cfg.ValuesFromProto(config.GetExtensions())[key]
}

func connectionString(section map[string]any, key string) string {
	value, _ := section[key].(string)
	return value
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
