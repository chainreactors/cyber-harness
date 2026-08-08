//go:build full

package scan

import (
	"context"
	"math"
	"net/url"
	"strings"
	"sync"

	browserutil "github.com/chainreactors/aiscan/pkg/browser"
	"github.com/projectdiscovery/gologger"
	"github.com/projectdiscovery/gologger/levels"
	"github.com/projectdiscovery/katana/pkg/engine"
	"github.com/projectdiscovery/katana/pkg/engine/headless"
	"github.com/projectdiscovery/katana/pkg/engine/standard"
	katanaoutput "github.com/projectdiscovery/katana/pkg/output"
	katanatypes "github.com/projectdiscovery/katana/pkg/types"
	"github.com/projectdiscovery/katana/pkg/utils/queue"

	"github.com/chainreactors/aiscan/tools/scan/pipeline"
)

const (
	capKatanaCrawl = "katana_crawl"
	capKatanaDeep  = "katana_deep"
)

func init() {
	RegisterProfileExtender(func(mode string, p *profile) {
		switch mode {
		case scanModeQuick:
			p.Capabilities[capKatanaCrawl] = struct{}{}
			if p.CrawlDepth > 1 {
				p.CrawlDepth = 1
			}
		case scanModeFull:
			p.Capabilities[capKatanaCrawl] = struct{}{}
			p.Capabilities[capKatanaDeep] = struct{}{}
		}
	})

	RegisterCapabilityBuilder(func(c *Command, f flags, opts scanOptions, p profile) []pipeline.Capability {
		var caps []pipeline.Capability
		katanaWebRoutes := wrapRoutes(acceptsTarget(targetWeb), webSources()...)
		if p.Enabled(capKatanaCrawl) {
			depth := p.CrawlDepth
			if depth <= 0 {
				depth = 2
			}
			caps = append(caps, wrapCapability(
				capKatanaCrawl,
				katanaWebRoutes,
				2,
				func(ctx context.Context, e event, emit func(event)) {
					runKatanaCrawl(ctx, c, e, depth, false, emit)
				},
			))
		}
		if p.Enabled(capKatanaDeep) {
			caps = append(caps, wrapCapability(
				capKatanaDeep,
				katanaWebRoutes,
				1,
				func(ctx context.Context, e event, emit func(event)) {
					runKatanaCrawl(ctx, c, e, 3, true, emit)
				},
			))
		}
		return caps
	})
}

func runKatanaCrawl(ctx context.Context, c *Command, e event, depth int, jsMode bool, emit func(event)) {
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
	handleResult := func(r *katanaoutput.Result) {
		if r == nil || r.Request == nil || r.Request.URL == "" {
			return
		}
		discoveredURL := r.Request.URL

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

		emit(targetEvent(source, wt.Raw, newWebTarget(wt.Raw, discoveredURL, wt.HostHeader)))
	}

	options := &katanatypes.Options{
		MaxDepth:               depth,
		FieldScope:             "rdn",
		BodyReadSize:           math.MaxInt,
		RateLimit:              150,
		Strategy:               queue.DepthFirst.String(),
		Silent:                 true,
		ScrapeJSResponses:      jsMode,
		ScrapeJSLuiceResponses: jsMode,
		Headless:               jsMode,
		Timeout:                10,
		TimeStable:             1,
		MaxFailureCount:        10,
		PageLoadStrategy:       "heuristic",
		DOMWaitTime:            5,
		Concurrency:            10,
		Parallelism:            10,
		OnResult: func(r katanaoutput.Result) {
			handleResult(&r)
		},
	}
	if c.Proxy != "" {
		options.Proxy = c.Proxy
	}
	if jsMode {
		binary, err := browserutil.Discover()
		if err != nil {
			emitError(emit, source, "katana browser discovery: %v", err)
			return
		}
		if binary.Path != "" {
			options.SystemChromePath = binary.Path
			options.UseInstalledChrome = true
		}
	}

	gologger.DefaultLogger.SetMaxLevel(levels.LevelSilent)
	crawlerOptions, err := katanatypes.NewCrawlerOptions(options)
	if err != nil {
		gologger.DefaultLogger.SetMaxLevel(levels.LevelWarning)
		emitError(emit, source, "katana init %s: %v", wt.URL, err)
		return
	}
	crawlerOptions.OutputWriter = &scanResultWriter{onResult: handleResult}
	defer func() {
		crawlerOptions.Close()
		gologger.DefaultLogger.SetMaxLevel(levels.LevelWarning)
	}()

	var crawler engine.Engine
	if jsMode {
		crawler, err = headless.New(crawlerOptions)
	} else {
		crawler, err = standard.New(crawlerOptions)
	}
	if err != nil {
		emitError(emit, source, "katana create %s: %v", wt.URL, err)
		return
	}
	defer crawler.Close()

	if err := crawler.Crawl(wt.URL); err != nil {
		if ctx.Err() == nil {
			emitError(emit, source, "katana crawl %s: %v", wt.URL, err)
		}
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

type scanResultWriter struct {
	onResult func(*katanaoutput.Result)
}

func (w *scanResultWriter) Close() error { return nil }
func (w *scanResultWriter) Write(result *katanaoutput.Result) error {
	if w.onResult != nil {
		w.onResult(result)
	}
	return nil
}
func (w *scanResultWriter) WriteErr(_ *katanaoutput.Error) error { return nil }
