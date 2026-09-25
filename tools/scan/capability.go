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

func scanCapability(name string, routes []pipeline.Route[event], worker int, run func(context.Context, event, func(event))) pipeline.Capability[event] {
	return pipeline.Capability[event]{
		Name:   name,
		Routes: routes,
		Worker: worker,
		Run:    run,
	}
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
	derivePerInvocationThreads(&flags, gogoCapacity, otherCapacity)

	var capabilities []pipeline.Capability[event]
	gogoBuilt := false
	sprayBuilt := false
	weakpassBuilt := false

	if profile.Enabled(capGogoPortscan) && c.engines.Gogo != nil {
		gogoBuilt = true
		capabilities = append(capabilities, scanCapability(
			capGogoPortscan,
			routes(acceptsTarget(targetScan), ""),
			capWorkers(gogoCapacity, flags.Threads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runPortDiscoveryCapability(ctx, flags, e.Target, emit)
			},
		))
	}

	addSpray := func(name string, sopts engine.SprayCheckOptions, sources ...string) {
		if !profile.Enabled(name) || c.engines.Spray == nil {
			return
		}
		sprayBuilt = true
		capabilities = append(capabilities, scanCapability(
			name,
			routes(acceptsTarget(targetWeb), sources...),
			capWorkers(otherCapacity, flags.SprayThreads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runSprayCapability(ctx, flags, e.Target, name, sopts, emit)
			},
		))
	}

	addSpray(capSprayCheck, engine.SprayCheckOptions{Finger: true}, "", capGogoPortscan, capSprayCrawl)

	if profile.Enabled(capCoreWeb) {
		capabilities = append(capabilities, scanCapability(
			capCoreWeb,
			routes(acceptsTarget(targetWebProbe), capSprayCheck, capSprayPlugins, capSprayBrute),
			2,
			func(_ context.Context, e event, emit func(event)) {
				target, ok := e.Target.(webProbeTarget)
				if ok && reportableSprayResultForCapability(target.Result, e.Source) {
					deriveWebProbeResult(flags.BroadPOC, e.Source, target.Result, emit)
				}
			},
		))
	}

	addSpray(capSprayPlugins, engine.SprayCheckOptions{
		CommonPlugin: true,
		BakPlugin:    true,
		ActivePlugin: true,
		Finger:       true,
	}, "", capGogoPortscan)

	if profile.Enabled(capSprayCrawl) && c.engines.Spray != nil {
		sprayBuilt = true
		capabilities = append(capabilities, scanCapability(
			capSprayCrawl,
			routes(acceptsTarget(targetWeb), "", capGogoPortscan),
			capWorkers(otherCapacity, flags.SprayThreads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runSprayCapability(ctx, flags, e.Target, capSprayCrawl, engine.SprayCheckOptions{Crawl: true, CrawlDepth: profile.CrawlDepth}, emit)
			},
		))
	}

	addSpray(capSprayBrute, engine.SprayCheckOptions{DefaultDict: true}, "", capGogoPortscan)

	if profile.Enabled(capZombieWeakpass) && c.engines.Zombie != nil {
		weakpassBuilt = true
		capabilities = append(capabilities, scanCapability(
			capHTTPBasicAuth,
			routes(acceptsTarget(targetWebProbe), capSprayCheck, capSprayPlugins),
			capWorkers(otherCapacity, flags.ZombieThreads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runHTTPBasicAuthCapability(ctx, flags, e, emit)
			},
		))
		capabilities = append(capabilities, scanCapability(
			capZombieWeakpass,
			routes(acceptsTarget(targetWeakpass), "", capGogoPortscan, capCoreWeb, capHTTPBasicAuth),
			capWorkers(otherCapacity, flags.ZombieThreads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runWeakpassCapability(ctx, flags, e.Target, emit)
			},
		))
	}

	if profile.Enabled(capNeutronPOC) && c.engines.Neutron != nil {
		capabilities = append(capabilities, scanCapability(
			capNeutronPOC,
			routes(acceptsTarget(targetPOC), capGogoPortscan, capCoreWeb),
			capWorkers(otherCapacity, 1),
			func(ctx context.Context, e event, emit func(event)) {
				c.runPOCCapability(ctx, flags, e.Target, emit)
			},
		))
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

func derivePerInvocationThreads(f *flags, gogoCapacity, otherCapacity int) {
	f.Threads = defaultGogoThreads
	if gogoCapacity > 0 && gogoCapacity < f.Threads {
		f.Threads = gogoCapacity
	}
	f.SprayThreads = defaultSprayThreads
	if otherCapacity > 0 && otherCapacity < f.SprayThreads {
		f.SprayThreads = otherCapacity
	}
	f.ZombieThreads = defaultZombieThreads
	if otherCapacity > 0 && otherCapacity < f.ZombieThreads {
		f.ZombieThreads = otherCapacity
	}
}

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
