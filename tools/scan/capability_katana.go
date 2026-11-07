package scan

import (
	"context"
	"net/url"
	"strings"
	"sync"

	"github.com/chainreactors/cyber/tools/scan/pipeline"
)

const (
	capKatanaCrawl = "katana_crawl"
	capKatanaDeep  = "katana_deep"
)

func (c *Command) extendKatanaProfile(mode string, p *profile) {
	if c.crawler == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", scanModeQuick:
		p.Capabilities[capKatanaCrawl] = struct{}{}
		if p.CrawlDepth > 1 {
			p.CrawlDepth = 1
		}
	case scanModeFull:
		p.Capabilities[capKatanaCrawl] = struct{}{}
		p.Capabilities[capKatanaDeep] = struct{}{}
	}
}

func (c *Command) buildKatanaCapabilities(p profile) []pipeline.Capability[event] {
	var caps []pipeline.Capability[event]
	katanaWebRoutes := routes(acceptsTarget(targetWeb), "", capGogoPortscan)
	if p.Enabled(capKatanaCrawl) {
		depth := p.CrawlDepth
		if depth <= 0 {
			depth = 2
		}
		caps = append(caps, pipeline.Capability[event]{
			Name:   capKatanaCrawl,
			Routes: katanaWebRoutes,
			Worker: 2,
			Run: func(ctx context.Context, e event, emit func(event)) {
				runKatanaCrawl(ctx, c, e, depth, false, emit)
			},
		})
	}
	if p.Enabled(capKatanaDeep) {
		caps = append(caps, pipeline.Capability[event]{
			Name:   capKatanaDeep,
			Routes: katanaWebRoutes,
			Worker: 1,
			Run: func(ctx context.Context, e event, emit func(event)) {
				runKatanaCrawl(ctx, c, e, 3, true, emit)
			},
		})
	}
	return caps
}

func runKatanaCrawl(ctx context.Context, c *Command, e event, depth int, jsMode bool, emit func(event)) {
	if c.crawler == nil {
		return
	}
	wt, ok := e.Target.(webTarget)
	if !ok || wt.URL == "" {
		return
	}

	source := capKatanaCrawl
	if jsMode {
		source = capKatanaDeep
	}

	seedRDN := rootDomainName(wt.URL)
	seedNorm := strings.TrimRight(strings.ToLower(wt.URL), "/")

	var mu sync.Mutex
	seen := make(map[string]struct{})
	handleResult := func(discoveredURL string) {
		if discoveredURL == "" {
			return
		}

		if seedRDN != "" && !sameRootDomain(discoveredURL, seedRDN) {
			return
		}
		if strings.TrimRight(strings.ToLower(discoveredURL), "/") == seedNorm {
			return
		}

		mu.Lock()
		if _, dup := seen[discoveredURL]; dup {
			mu.Unlock()
			return
		}
		seen[discoveredURL] = struct{}{}
		mu.Unlock()

		emit(targetEvent(source, newWebTarget(discoveredURL, wt.HostHeader)))
	}

	if err := c.crawler(ctx, wt.URL, wt.HostHeader, depth, jsMode, c.proxyForContext(ctx), handleResult); err != nil && ctx.Err() == nil {
		emitError(emit, source, "%v", err)
	}
}

func rootDomainName(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	host := parsed.Hostname()
	parts := strings.Split(host, ".")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], ".")
	}
	return host
}

func sameRootDomain(rawURL, rdn string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	host := parsed.Hostname()
	return host == rdn || strings.HasSuffix(host, "."+rdn)
}
