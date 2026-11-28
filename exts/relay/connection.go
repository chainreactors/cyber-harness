package relay

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/chainreactors/cyber/core/resource"
	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	relaytool "github.com/chainreactors/cyber/tools/relay"
)

const connectionTimeout = 20 * time.Second

// Declare registers the gateway.relay connection check. The schema itself is
// declared with the other gateway kinds.
func Declare(resources *resource.Registry) error {
	_, err := resource.Add(resources, cfg.Connection{Section: "gateway." + Kind, Test: testConnection})
	return err
}

func testConnection(ctx context.Context, in, stored *types.DistributeConfig) []*types.ConnectionCheck {
	cached := cfg.ValuesFromProto(stored.GetExtensions())["gateway"]
	services := cfg.ValuesFromProto(in.GetExtensions())["gateway"]
	if services == nil {
		services = cached
	}
	var checks []*types.ConnectionCheck
	for name, raw := range services {
		fields, _ := raw.(map[string]any)
		if kind, _ := fields["kind"].(string); kind != Kind && (kind != "" || name != Kind) {
			continue
		}
		if fields["enabled"] == false {
			continue
		}
		fields = cfg.CloneDocument(fields)
		delete(fields, "kind")
		delete(fields, "enabled")
		// A partial edit keeps the stored key, as saving the configuration does.
		if previous, _ := cached[name].(map[string]any); previous != nil && (fields["key"] == nil || fields["key"] == "") {
			previousKind, _ := previous["kind"].(string)
			if previousKind == "" {
				previousKind = name
			}
			if previousKind == Kind {
				fields["key"] = previous["key"]
			}
		}
		checks = append(checks, cfg.Check(name, func() (string, error) {
			value, err := Section().Decode(fields)
			if err != nil {
				return "", err
			}
			api := value.(*cfg.API)
			client, err := relaytool.New(relaytool.Config{BaseURL: api.BaseURL, Key: api.Key})
			if err != nil {
				return "", err
			}
			probe, cancel := context.WithTimeout(ctx, connectionTimeout)
			defer cancel()
			// Explain spends no upstream credit. Every authenticated call needs
			// a scope, so a 403 still proves the key is valid.
			_, err = client.ExplainSearch(probe, relaytool.SearchRequest{Query: `ip="1.1.1.1"`})
			var relayErr *relaytool.Error
			if errors.As(err, &relayErr) && relayErr.Status == http.StatusForbidden &&
				(relayErr.Code == "SCOPE_DENIED" || relayErr.Code == "INSUFFICIENT_SCOPE") {
				return "authenticated; the key has no asset-search scope", nil
			}
			if err != nil {
				return "", err
			}
			return "authenticated", nil
		}))
	}
	return checks
}
