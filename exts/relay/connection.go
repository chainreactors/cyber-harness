package relay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
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
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)

	type candidate struct {
		name   string
		fields map[string]any
	}
	var checks []*types.ConnectionCheck
	var enabled []candidate
	for _, name := range names {
		fields, ok := services[name].(map[string]any)
		if !ok {
			if name == Kind {
				checks = append(checks, cfg.Check(name, func() (string, error) {
					return "", fmt.Errorf("gateway.%s must be an object", name)
				}))
			}
			continue
		}
		if rawKind, hasKind := fields["kind"]; hasKind {
			kind, ok := rawKind.(string)
			if !ok || kind == "" || strings.ContainsAny(kind, ".:/\\ \t\r\n") {
				if name == Kind {
					checks = append(checks, cfg.Check(name, func() (string, error) {
						return "", fmt.Errorf("gateway.%s.kind must name a service kind", name)
					}))
				}
				continue
			}
			if kind != Kind {
				continue
			}
		} else if name != Kind {
			continue
		}
		if rawEnabled, hasEnabled := fields["enabled"]; hasEnabled {
			flag, ok := rawEnabled.(bool)
			if !ok {
				checks = append(checks, cfg.Check(name, func() (string, error) {
					return "", fmt.Errorf("gateway.%s.enabled must be a boolean", name)
				}))
				continue
			}
			if !flag {
				continue
			}
		}
		enabled = append(enabled, candidate{name: name, fields: fields})
	}
	if len(enabled) > 1 {
		selected := make([]string, 0, len(enabled))
		for _, item := range enabled {
			selected = append(selected, item.name)
		}
		return append(checks, cfg.Check(Kind, func() (string, error) {
			return "", fmt.Errorf("multiple relay services configured (%s); only one is allowed", strings.Join(selected, ", "))
		}))
	}
	for _, item := range enabled {
		name := item.name
		fields := cfg.CloneDocument(item.fields)
		delete(fields, "kind")
		delete(fields, "enabled")
		// A partial edit keeps the stored key only when the service kind and
		// endpoint are unchanged, so a changed base_url cannot exfiltrate it.
		if previous, _ := cached[name].(map[string]any); previous != nil && (fields["key"] == nil || fields["key"] == "") {
			previousKind, _ := previous["kind"].(string)
			if previousKind == "" {
				previousKind = name
			}
			if previousKind == Kind && sameBaseURL(fields["base_url"], previous["base_url"]) {
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
			const probeQuery = `ip="1.1.1.1"`
			// Explain spends no upstream credit. Every authenticated call needs
			// a scope, so a scope-denial 403 still proves the key is valid.
			resp, err := client.ExplainSearch(probe, relaytool.SearchRequest{Query: probeQuery})
			var relayErr *relaytool.Error
			if errors.As(err, &relayErr) && relayErr.Status == http.StatusForbidden &&
				(relayErr.Code == "SCOPE_DENIED" || relayErr.Code == "INSUFFICIENT_SCOPE") {
				return "authenticated; the key has no asset-search scope", nil
			}
			if err != nil {
				return "", err
			}
			if resp == nil || resp.Query != probeQuery {
				return "", fmt.Errorf("unexpected response from relay explain endpoint")
			}
			return "authenticated", nil
		}))
	}
	return checks
}

func sameBaseURL(next, prev any) bool {
	n, ok1 := next.(string)
	p, ok2 := prev.(string)
	if !ok1 || !ok2 {
		return false
	}
	u1, err1 := url.Parse(strings.TrimRight(strings.TrimSpace(n), "/"))
	u2, err2 := url.Parse(strings.TrimRight(strings.TrimSpace(p), "/"))
	if err1 != nil || err2 != nil || u1.Hostname() == "" || u2.Hostname() == "" {
		return false
	}
	return u1.User == nil && u2.User == nil &&
		strings.EqualFold(u1.Scheme, u2.Scheme) &&
		strings.EqualFold(u1.Host, u2.Host) &&
		u1.Path == u2.Path &&
		u1.RawQuery == u2.RawQuery &&
		u1.ForceQuery == u2.ForceQuery &&
		u1.Fragment == u2.Fragment
}
