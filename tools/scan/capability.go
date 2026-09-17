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

// These aliases expose the existing scan domain to statically supplied builders.
type Flags = flags
type ScanOptions = scanOptions
type Profile = profile
type CapabilityBuilder func(*Command, Flags, ScanOptions, Profile) []pipeline.Capability[event]
type ProfileExtender func(string, *Profile)

func WithCapabilityBuilders(builders ...CapabilityBuilder) Option {
	copied := append([]CapabilityBuilder(nil), builders...)
	return func(c *Command) { c.builders = append(c.builders, copied...) }
}
func WithProfileExtenders(extenders ...ProfileExtender) Option {
	copied := append([]ProfileExtender(nil), extenders...)
	return func(c *Command) { c.profileExtenders = append(c.profileExtenders, copied...) }
}

func acceptsTarget(kinds ...targetKind) func(event) bool {
	set := make(map[targetKind]struct{}, len(kinds))
	for _, kind := range kinds {
		set[kind] = struct{}{}
	}
	return func(e event) bool {
		if e.Kind != eventTarget || e.Target == nil {
			return false
		}
		_, ok := set[e.Target.Kind()]
		return ok
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

// webSources returns the sources that produce webTarget events for probing capabilities.
func webSources() []string {
	return []string{"", capGogoPortscan}
}

// crawlSources returns the sources whose output feeds into spray_check for enrichment.
func crawlSources() []string {
	return []string{capSprayCrawl}
}

func (c *Command) buildCapabilities(flags flags, opts scanOptions, profile profile) []pipeline.Capability[event] {
	if c.engines == nil {
		c.engines = &engine.Set{}
	}
	c.engines.Capacity = distributeCapacity(flags.Thread)
	derivePerInvocationThreads(&flags, c.engines.Capacity)

	var capabilities []pipeline.Capability[event]
	gogoBuilt := false
	sprayBuilt := false
	weakpassBuilt := false

	if profile.Enabled(capGogoPortscan) && hasGogo(c.engines) {
		gogoBuilt = true
		capabilities = append(capabilities, scanCapability(
			capGogoPortscan,
			routes(acceptsTarget(targetScan), ""),
			capWorkers(c.engines.Capacity.Gogo, flags.Threads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runPortDiscoveryCapability(ctx, opts.Discovery, profile, e.Target, emit)
			},
		))
	}

	addSpray := func(name string, sopts engine.SprayCheckOptions, sources []string) {
		if !profile.Enabled(name) || !hasSpray(c.engines) {
			return
		}
		// The final call-scoped route is applied in runSprayCapability, where the
		// pipeline context is available. Keep the startup value here for direct
		// unit callers that do not install an invocation context.
		sopts.Proxy = c.Proxy
		sprayBuilt = true
		capabilities = append(capabilities, sprayCapability(c, flags, opts.Web, name, sources, sopts, c.runSprayCapability))
	}

	sprayCheckSources := append(webSources(), crawlSources()...)
	addSpray(capSprayCheck, engine.SprayCheckOptions{Finger: true}, sprayCheckSources)

	if profile.Enabled(capCoreWeb) {
		capabilities = append(capabilities, scanCapability(
			capCoreWeb,
			routes(acceptsTarget(targetWebProbe), capSprayCheck, capSprayPlugins, capSprayBrute),
			2,
			func(ctx context.Context, e event, emit func(event)) {
				runWebResultAnalysisCapability(ctx, profile, e.Target, emit)
			},
		))
	}

	addSpray(capSprayPlugins, engine.SprayCheckOptions{
		CommonPlugin: true,
		BakPlugin:    true,
		ActivePlugin: true,
		Finger:       true,
	}, webSources())

	if profile.Enabled(capSprayCrawl) && hasSpray(c.engines) {
		sprayBuilt = true
		capabilities = append(capabilities, scanCapability(
			capSprayCrawl,
			routes(acceptsTarget(targetWeb), webSources()...),
			capWorkers(c.engines.Capacity.Spray, flags.SprayThreads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runSprayCapability(ctx, flags, opts.Web, e.Target, capSprayCrawl, engine.SprayCheckOptions{Crawl: true, CrawlDepth: profile.CrawlDepth, Proxy: c.proxyForContext(ctx)}, emit)
			},
		))
	}

	addSpray(capSprayBrute, engine.SprayCheckOptions{DefaultDict: true}, webSources())

	if profile.Enabled(capZombieWeakpass) && hasZombie(c.engines) {
		weakpassBuilt = true
		capabilities = append(capabilities, scanCapability(
			capHTTPBasicAuth,
			routes(acceptsTarget(targetWebProbe), capSprayCheck, capSprayPlugins),
			capWorkers(c.engines.Capacity.Zombie, flags.ZombieThreads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runHTTPBasicAuthCapability(ctx, flags, e.Target, emit)
			},
		))
		capabilities = append(capabilities, scanCapability(
			capZombieWeakpass,
			routes(acceptsTarget(targetWeakpass), "", capGogoPortscan, capCoreWeb, capHTTPBasicAuth),
			capWorkers(c.engines.Capacity.Zombie, flags.ZombieThreads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runWeakpassCapability(ctx, flags, opts.Credentials, e.Target, emit)
			},
		))
	}

	if profile.Enabled(capNeutronPOC) && hasNeutron(c.engines) {
		capabilities = append(capabilities, scanCapability(
			capNeutronPOC,
			routes(acceptsTarget(targetPOC), capGogoPortscan, capCoreWeb),
			capWorkers(c.engines.Capacity.Neutron, 1),
			func(ctx context.Context, e event, emit func(event)) {
				c.runPOCCapability(ctx, flags, e.Target, emit)
			},
		))
	}

	if opts.hasDiscoveryOverrides() && !gogoBuilt {
		c.Logger.Warnf("scan capability=%s option=port status=ignored reason=engine_unavailable", capGogoPortscan)
	}
	if opts.hasWebOverrides() && !sprayBuilt {
		c.Logger.Warnf("scan capability=web_probe option=dict,rule,word,default-dict,advance status=ignored reason=engine_unavailable")
	}
	if opts.hasWeakpassOverrides() && !weakpassBuilt {
		c.Logger.Warnf("scan capability=%s option=user,pwd status=ignored reason=engine_unavailable", capZombieWeakpass)
	}

	for _, builder := range c.builders {
		capabilities = append(capabilities, builder(c, flags, opts, profile)...)
	}

	return capabilities
}

func sprayCapability(c *Command, flags flags, web webOptions, name string, sources []string, opts engine.SprayCheckOptions, run func(context.Context, flags, webOptions, target, string, engine.SprayCheckOptions, func(event))) pipeline.Capability[event] {
	return scanCapability(
		name,
		routes(acceptsTarget(targetWeb), sources...),
		capWorkers(c.engines.Capacity.Spray, flags.SprayThreads),
		func(ctx context.Context, e event, emit func(event)) {
			run(ctx, flags, web, e.Target, name, opts, emit)
		},
	)
}

const (
	defaultGogoThreads   = 500
	defaultSprayThreads  = 20
	defaultZombieThreads = 100
)

func derivePerInvocationThreads(f *flags, cap engine.CapacityConfig) {
	f.Threads = defaultGogoThreads
	if cap.Gogo > 0 && cap.Gogo < f.Threads {
		f.Threads = cap.Gogo
	}
	f.SprayThreads = defaultSprayThreads
	if cap.Spray > 0 && cap.Spray < f.SprayThreads {
		f.SprayThreads = cap.Spray
	}
	f.ZombieThreads = defaultZombieThreads
	if cap.Zombie > 0 && cap.Zombie < f.ZombieThreads {
		f.ZombieThreads = cap.Zombie
	}
}

func distributeCapacity(total int) engine.CapacityConfig {
	if total <= 0 {
		total = 1000
	}
	return engine.CapacityConfig{
		Gogo:    total * 8 / 10,
		Spray:   total / 10,
		Zombie:  total / 10,
		Neutron: total / 10,
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

func hasGogo(engineSet *engine.Set) bool {
	return engineSet != nil && engineSet.Gogo != nil
}

func hasSpray(engineSet *engine.Set) bool {
	return engineSet != nil && engineSet.Spray != nil
}

func hasZombie(engineSet *engine.Set) bool {
	return engineSet != nil && engineSet.Zombie != nil
}

func hasNeutron(engineSet *engine.Set) bool {
	return engineSet != nil && engineSet.Neutron != nil
}
