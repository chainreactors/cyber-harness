package scan

import (
	"context"

	"github.com/chainreactors/cyber/tools/scan/engine"
	"github.com/chainreactors/cyber/tools/scan/pipeline"
)

const (
	capGogoPortscan   = "gogo_portscan"
	capSprayCheck     = "spray_check"
	capCoreWeb        = "core_web"
	capSprayPlugins   = "spray_plugins"
	capSprayCrawl     = "spray_crawl"
	capSprayBrute     = "spray_brute"
	capHTTPBasicAuth  = "http_basic_auth"
	capZombieWeakpass = "zombie_weakpass"
	capNeutronPOC     = "neutron_poc"
)

func acceptsTarget(kind targetKind) func(event) bool {
	return func(e event) bool {
		return e.Kind == eventTarget && e.Target != nil && e.Target.Kind() == kind
	}
}

func routes(accept func(event) bool, sources ...string) []pipeline.Route[event] {
	out := make([]pipeline.Route[event], len(sources))
	for i, source := range sources {
		out[i] = pipeline.Route[event]{From: source, Accept: accept}
	}
	return out
}

func (c *Command) buildCapabilities(flags flags, profile profile) []pipeline.Capability[event] {
	if c.engines == nil {
		c.engines = &engine.Set{}
	}
	total := flags.Thread
	if total <= 0 {
		total = 1000
	}
	gogoCapacity := total * 8 / 10
	otherCapacity := total / 10
	flags.Threads = defaultGogoThreads
	if gogoCapacity > 0 && gogoCapacity < flags.Threads {
		flags.Threads = gogoCapacity
	}
	flags.SprayThreads = defaultSprayThreads
	if otherCapacity > 0 && otherCapacity < flags.SprayThreads {
		flags.SprayThreads = otherCapacity
	}
	flags.ZombieThreads = defaultZombieThreads
	if otherCapacity > 0 && otherCapacity < flags.ZombieThreads {
		flags.ZombieThreads = otherCapacity
	}

	var capabilities []pipeline.Capability[event]
	gogoBuilt := false
	sprayBuilt := false
	weakpassBuilt := false

	if profile.Enabled(capGogoPortscan) && c.engines.Gogo != nil {
		gogoBuilt = true
		capabilities = append(capabilities, pipeline.Capability[event]{
			Name:   capGogoPortscan,
			Routes: routes(acceptsTarget(targetScan), ""),
			Worker: capWorkers(gogoCapacity, flags.Threads),
			Run: func(ctx context.Context, e event, emit func(event)) {
				c.scanPorts(ctx, flags, e.Target, emit)
			},
		})
	}

	addSpray := func(name string, sopts engine.SprayCheckOptions, sources ...string) {
		if !profile.Enabled(name) || c.engines.Spray == nil {
			return
		}
		sprayBuilt = true
		capabilities = append(capabilities, pipeline.Capability[event]{
			Name:   name,
			Routes: routes(acceptsTarget(targetWeb), sources...),
			Worker: capWorkers(otherCapacity, flags.SprayThreads),
			Run: func(ctx context.Context, e event, emit func(event)) {
				c.probeWeb(ctx, flags, e.Target, name, sopts, emit)
			},
		})
	}

	addSpray(capSprayCheck, engine.SprayCheckOptions{Finger: true}, "", capGogoPortscan, capSprayCrawl)

	if profile.Enabled(capCoreWeb) {
		capabilities = append(capabilities, pipeline.Capability[event]{
			Name:   capCoreWeb,
			Routes: routes(acceptsTarget(targetWebProbe), capSprayCheck, capSprayPlugins, capSprayBrute),
			Worker: 2,
			Run: func(_ context.Context, e event, emit func(event)) {
				target, ok := e.Target.(webProbeTarget)
				if ok && reportableSprayResultForCapability(target.Result, e.Source) {
					deriveWebProbeResult(flags.BroadPOC, e.Source, e, emit)
				}
			},
		})
	}

	addSpray(capSprayPlugins, engine.SprayCheckOptions{
		CommonPlugin: true,
		BakPlugin:    true,
		ActivePlugin: true,
		Finger:       true,
	}, "", capGogoPortscan)

	if profile.Enabled(capSprayCrawl) && c.engines.Spray != nil {
		sprayBuilt = true
		capabilities = append(capabilities, pipeline.Capability[event]{
			Name:   capSprayCrawl,
			Routes: routes(acceptsTarget(targetWeb), "", capGogoPortscan),
			Worker: capWorkers(otherCapacity, flags.SprayThreads),
			Run: func(ctx context.Context, e event, emit func(event)) {
				c.probeWeb(ctx, flags, e.Target, capSprayCrawl, engine.SprayCheckOptions{Crawl: true, CrawlDepth: profile.CrawlDepth}, emit)
			},
		})
	}

	addSpray(capSprayBrute, engine.SprayCheckOptions{DefaultDict: true}, "", capGogoPortscan)

	if profile.Enabled(capZombieWeakpass) && c.engines.Zombie != nil {
		weakpassBuilt = true
		capabilities = append(capabilities, pipeline.Capability[event]{
			Name:   capHTTPBasicAuth,
			Routes: routes(acceptsTarget(targetWebProbe), capSprayCheck, capSprayPlugins),
			Worker: capWorkers(otherCapacity, flags.ZombieThreads),
			Run: func(ctx context.Context, e event, emit func(event)) {
				c.checkBasicAuth(ctx, flags, e, emit)
			},
		})
		capabilities = append(capabilities, pipeline.Capability[event]{
			Name:   capZombieWeakpass,
			Routes: routes(acceptsTarget(targetWeakpass), "", capGogoPortscan, capCoreWeb, capHTTPBasicAuth),
			Worker: capWorkers(otherCapacity, flags.ZombieThreads),
			Run: func(ctx context.Context, e event, emit func(event)) {
				c.checkCredentials(ctx, flags, e.Target, emit)
			},
		})
	}

	if profile.Enabled(capNeutronPOC) && c.engines.Neutron != nil {
		capabilities = append(capabilities, pipeline.Capability[event]{
			Name:   capNeutronPOC,
			Routes: routes(acceptsTarget(targetPOC), capGogoPortscan, capCoreWeb),
			Worker: capWorkers(otherCapacity, 1),
			Run: func(ctx context.Context, e event, emit func(event)) {
				c.checkPOCs(ctx, flags, e.Target, emit)
			},
		})
	}

	if flags.Ports != "" && !gogoBuilt {
		c.Logger.Warnf("scan capability=%s option=port status=ignored reason=engine_unavailable", capGogoPortscan)
	}
	if (len(flags.Dictionaries) > 0 || len(flags.Rules) > 0 || flags.Word != "" || flags.DefaultDict || flags.Advance) && !sprayBuilt {
		c.Logger.Warnf("scan capability=web_probe option=dict,rule,word,default-dict,advance status=ignored reason=engine_unavailable")
	}
	if (len(flags.Users) > 0 || len(flags.Passwords) > 0) && !weakpassBuilt {
		c.Logger.Warnf("scan capability=%s option=user,pwd status=ignored reason=engine_unavailable", capZombieWeakpass)
	}

	return append(capabilities, c.buildKatanaCapabilities(profile)...)
}

const (
	defaultGogoThreads   = 500
	defaultSprayThreads  = 20
	defaultZombieThreads = 100
)

func capWorkers(capacity, threadsPerInvocation int) int {
	if capacity <= 0 || threadsPerInvocation <= 0 {
		return 2
	}
	w := capacity / threadsPerInvocation
	if w < 1 {
		w = 1
	}
	if w > 16 {
		w = 16
	}
	return w
}
