package scan

import (
	"context"

	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
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

// CapabilityBuilder builds additional pipeline capabilities for a given
// profile. Build-tagged packages (e.g. katana) register builders via init()
// so the scan pipeline gains optional capabilities without the core scan
// package importing them.
type CapabilityBuilder func(c *Command, flags flags, opts scanOptions, profile profile) []pipeline.Capability

var extraCapabilityBuilders []CapabilityBuilder

// RegisterCapabilityBuilder adds an optional capability builder that will be
// invoked during buildCapabilities. Intended for build-tagged init() calls.
func RegisterCapabilityBuilder(fn CapabilityBuilder) {
	extraCapabilityBuilders = append(extraCapabilityBuilders, fn)
}

// ProfileExtender modifies a profile's capability set for a given mode.
// Build-tagged packages register extenders to add optional capability names.
type ProfileExtender func(mode string, p *profile)

var profileExtenders []ProfileExtender

// RegisterProfileExtender adds a profile extender called during profileForMode.
func RegisterProfileExtender(fn ProfileExtender) {
	profileExtenders = append(profileExtenders, fn)
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

// webSources returns the sources that produce webTarget events for probing capabilities.
func webSources() []string {
	return []string{"", capGogoPortscan}
}

// crawlSources returns the sources whose output feeds into spray_check for enrichment.
func crawlSources() []string {
	return []string{capSprayCrawl}
}

func (c *Command) buildCapabilities(flags flags, opts scanOptions, profile profile) []pipeline.Capability {
	if c.engines == nil {
		c.engines = &engine.Set{}
	}
	c.engines.Capacity = distributeCapacity(flags.Thread)
	derivePerInvocationThreads(&flags, c.engines.Capacity)

	var capabilities []pipeline.Capability
	gogoBuilt := false
	sprayBuilt := false
	weakpassBuilt := false

	if profile.Enabled(capGogoPortscan) && hasGogo(c.engines) {
		gogoBuilt = true
		capabilities = append(capabilities, wrapCapability(
			capGogoPortscan,
			wrapRoutes(acceptsTarget(targetScan), ""),
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
		sopts.Proxy = c.Proxy
		sprayBuilt = true
		capabilities = append(capabilities, sprayCapability(c, flags, opts.Web, name, sources, sopts, c.runSprayCapability))
	}

	sprayCheckSources := append(webSources(), crawlSources()...)
	addSpray(capSprayCheck, engine.SprayCheckOptions{Finger: true}, sprayCheckSources)

	if profile.Enabled(capCoreWeb) {
		capabilities = append(capabilities, wrapCapability(
			capCoreWeb,
			wrapRoutes(acceptsTarget(targetWebProbe), capSprayCheck, capSprayPlugins, capSprayBrute),
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
		capabilities = append(capabilities, wrapCapability(
			capSprayCrawl,
			wrapRoutes(acceptsTarget(targetWeb), webSources()...),
			capWorkers(c.engines.Capacity.Spray, flags.SprayThreads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runSprayCapability(ctx, flags, opts.Web, e.Target, capSprayCrawl, engine.SprayCheckOptions{Crawl: true, CrawlDepth: profile.CrawlDepth, Proxy: c.Proxy}, emit)
			},
		))
	}

	addSpray(capSprayBrute, engine.SprayCheckOptions{DefaultDict: true}, webSources())

	if profile.Enabled(capZombieWeakpass) && hasZombie(c.engines) {
		weakpassBuilt = true
		capabilities = append(capabilities, wrapCapability(
			capHTTPBasicAuth,
			wrapRoutes(acceptsTarget(targetWebProbe), capSprayCheck, capSprayPlugins),
			capWorkers(c.engines.Capacity.Zombie, flags.ZombieThreads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runHTTPBasicAuthCapability(ctx, flags, e.Target, emit)
			},
		))
		capabilities = append(capabilities, wrapCapability(
			capZombieWeakpass,
			wrapRoutes(acceptsTarget(targetWeakpass), "", capGogoPortscan, capCoreWeb, capHTTPBasicAuth),
			capWorkers(c.engines.Capacity.Zombie, flags.ZombieThreads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runWeakpassCapability(ctx, flags, opts.Credentials, e.Target, emit)
			},
		))
	}

	if profile.Enabled(capNeutronPOC) && hasNeutron(c.engines) {
		capabilities = append(capabilities, wrapCapability(
			capNeutronPOC,
			wrapRoutes(acceptsTarget(targetPOC), capGogoPortscan, capCoreWeb),
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

	for _, builder := range extraCapabilityBuilders {
		capabilities = append(capabilities, builder(c, flags, opts, profile)...)
	}

	return capabilities
}

func sprayCapability(c *Command, flags flags, web webOptions, name string, sources []string, opts engine.SprayCheckOptions, run func(context.Context, flags, webOptions, target, string, engine.SprayCheckOptions, func(event))) pipeline.Capability {
	return wrapCapability(
		name,
		wrapRoutes(acceptsTarget(targetWeb), sources...),
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
