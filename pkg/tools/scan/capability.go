package scan

import (
	"context"

	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
)

const (
	capGogoPortscan   = "gogo_portscan"
	capSprayCheck     = "spray_check"
	capSprayFinger    = "spray_finger"
	capCoreWeb        = "core_web"
	capSprayCommon    = "spray_common"
	capSprayBackup    = "spray_backup"
	capSprayActive    = "spray_active"
	capSprayCrawl     = "spray_crawl"
	capSprayBrute     = "spray_brute"
	capZombieWeakpass = "zombie_weakpass"
	capNeutronPOC     = "neutron_poc"
	capAgentVerify    = "agent_verify"
)

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
			acceptsTarget(targetScan),
			capWorkers(c.engines.Capacity.Gogo, flags.Threads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runPortDiscoveryCapability(ctx, opts.Discovery, profile, e.Target, emit)
			},
		))
	}

	addSpray := func(name string, sopts engine.SprayCheckOptions) {
		if !profile.Enabled(name) || !hasSpray(c.engines) {
			return
		}
		sprayBuilt = true
		capabilities = append(capabilities, sprayCapability(c, flags, opts.Web, name, sopts, c.runSprayCapability))
	}

	addSpray(capSprayCheck, engine.SprayCheckOptions{})
	addSpray(capSprayFinger, engine.SprayCheckOptions{Finger: true})

	if profile.Enabled(capCoreWeb) {
		capabilities = append(capabilities, wrapCapability(
			capCoreWeb,
			acceptsTarget(targetWebProbe),
			2,
			func(ctx context.Context, e event, emit func(event)) {
				runWebResultAnalysisCapability(ctx, profile, e.Target, emit)
			},
		))
	}

	addSpray(capSprayCommon, engine.SprayCheckOptions{CommonPlugin: true})
	addSpray(capSprayBackup, engine.SprayCheckOptions{BakPlugin: true})
	addSpray(capSprayActive, engine.SprayCheckOptions{ActivePlugin: true, Finger: true})

	if profile.Enabled(capSprayCrawl) && hasSpray(c.engines) {
		sprayBuilt = true
		capabilities = append(capabilities, sprayCapability(c, flags, opts.Web, capSprayCrawl, engine.SprayCheckOptions{Crawl: true, CrawlDepth: profile.CrawlDepth}, c.runSprayCapability))
	}

	addSpray(capSprayBrute, engine.SprayCheckOptions{DefaultDict: true})

	if profile.Enabled(capZombieWeakpass) && hasZombie(c.engines) {
		weakpassBuilt = true
		capabilities = append(capabilities, wrapCapability(
			capZombieWeakpass,
			acceptsTarget(targetWeakpass),
			capWorkers(c.engines.Capacity.Zombie, flags.ZombieThreads),
			func(ctx context.Context, e event, emit func(event)) {
				c.runWeakpassCapability(ctx, flags, opts.Credentials, e.Target, emit)
			},
		))
	}

	if profile.Enabled(capNeutronPOC) && hasNeutron(c.engines) {
		capabilities = append(capabilities, wrapCapability(
			capNeutronPOC,
			acceptsTarget(targetPOC),
			capWorkers(c.engines.Capacity.Neutron, 1),
			func(ctx context.Context, e event, emit func(event)) {
				c.runPOCCapability(ctx, flags, e.Target, emit)
			},
		))
	}

	if opts.hasDiscoveryOverrides() && !gogoBuilt {
		c.logger.Warnf("scan %s port ignored unavailable", capGogoPortscan)
	}
	if opts.hasWebOverrides() && !sprayBuilt {
		c.logger.Warnf("scan web_probe dict,rule,word,default-dict,advance ignored unavailable")
	}
	if opts.hasWeakpassOverrides() && !weakpassBuilt {
		c.logger.Warnf("scan %s user,pwd ignored unavailable", capZombieWeakpass)
	}

	if verificationEnabled(flags.Verify) {
		if cap, ok := c.agentVerifyCapability(flags); ok {
			capabilities = append(capabilities, cap)
		}
	}
	return capabilities
}

func sprayCapability(c *Command, flags flags, web webOptions, name string, opts engine.SprayCheckOptions, run func(context.Context, flags, webOptions, target, string, engine.SprayCheckOptions, func(event))) pipeline.Capability {
	return wrapCapability(
		name,
		acceptsTarget(targetWeb),
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
