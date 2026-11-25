package search

import (
	"context"
	"fmt"
	"strings"
	"time"

	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	searchtools "github.com/chainreactors/cyber/tools/search"
)

const connectionTimeout = 20 * time.Second

func testConnection(ctx context.Context, in, stored *types.DistributeConfig) []*types.ConnectionCheck {
	incoming, current := in.GetExtensions(), stored.GetExtensions()
	searchKeys := fallbackString(incoming[ConfigKey].GetFields()["tavily_keys"].GetStringValue(), current[ConfigKey].GetFields()["tavily_keys"].GetStringValue())
	reconKeys := fallbackString(incoming["recon"].GetFields()["tavily_key"].GetStringValue(), current["recon"].GetFields()["tavily_key"].GetStringValue())
	keys := fallbackString(reconKeys, searchKeys)
	cached := cfg.ValuesFromProto(stored.GetExtensions())["gateway"]
	services := cached
	if incoming := cfg.ValuesFromProto(in.GetExtensions())["gateway"]; incoming != nil {
		services = incoming
	}
	var api cfg.API
	var decodeErr error
	found := false
	for name, raw := range services {
		fields, _ := raw.(map[string]any)
		kind, _ := fields["kind"].(string)
		if kind == "" {
			kind = name
		}
		if kind != "tavily" {
			continue
		}
		if fields["enabled"] == false {
			decodeErr = fmt.Errorf("tavily is disabled")
			break
		}
		if found {
			decodeErr = fmt.Errorf("select one Tavily service")
			break
		}
		found = true
		fields = cfg.CloneDocument(fields)
		delete(fields, "kind")
		delete(fields, "enabled")
		previous, _ := cached[name].(map[string]any)
		previousKind, _ := previous["kind"].(string)
		if previousKind == "" {
			previousKind = name
		}
		if previousKind == kind {
			if fields["key"] == nil || fields["key"] == "" {
				fields["key"] = previous["key"]
			}
			if _, exists := fields["base_url"]; !exists && previous["base_url"] != nil {
				fields["base_url"] = previous["base_url"]
			}
		}
		value, err := apiSection("tavily").Decode(fields)
		if err != nil {
			decodeErr = err
			break
		}
		api = *value.(*cfg.API)
	}
	if api.Key != "" {
		keys = api.Key
	}
	return []*types.ConnectionCheck{cfg.Check("tavily", func() (string, error) {
		if decodeErr != nil {
			return "", decodeErr
		}
		key := firstCSV(keys)
		if key == "" {
			return "", fmt.Errorf("no tavily api key configured")
		}
		probeCtx, cancel := context.WithTimeout(ctx, connectionTimeout)
		defer cancel()
		return searchtools.ProbeTavily(probeCtx, key, api.BaseURL, "")
	})}
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
