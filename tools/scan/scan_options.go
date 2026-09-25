package scan

import (
	"fmt"
	"strings"
)

const (
	scanQuickDefaultPorts = "all"
	scanFullDefaultPorts  = "-"
	scanGogoVersionLevel  = 1
	scanGogoExploitMode   = "auto"
)

func defaultDiscoveryPorts(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), scanModeFull) {
		return scanFullDefaultPorts
	}
	return scanQuickDefaultPorts
}

const (
	scanModeQuick = "quick"
	scanModeFull  = "full"
)

type profile struct {
	Capabilities map[string]struct{}
	CrawlDepth   int
}

func profileForMode(mode string) (profile, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = scanModeQuick
	}
	if mode != scanModeQuick && mode != scanModeFull {
		return profile{}, fmt.Errorf("unknown scan mode %q, expected quick or full", mode)
	}

	p := profile{
		Capabilities: capabilitySet(
			capGogoPortscan, capSprayCheck, capCoreWeb,
			capSprayCrawl, capZombieWeakpass, capNeutronPOC,
		),
		CrawlDepth: 2,
	}
	if mode == scanModeFull {
		p.Capabilities[capSprayPlugins] = struct{}{}
		p.Capabilities[capSprayBrute] = struct{}{}
	}
	extendKatanaProfile(mode, &p)
	return p, nil
}

func capabilitySet(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

func (p profile) Enabled(name string) bool {
	_, ok := p.Capabilities[name]
	return ok
}
