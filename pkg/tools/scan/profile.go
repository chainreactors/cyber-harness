package scan

import (
	"fmt"
	"strings"
)

const (
	scanModeQuick = "quick"
	scanModeFull  = "full"
)

type profile struct {
	Name          string
	Capabilities  map[string]struct{}
	CrawlDepth    int
	AllowBroadPOC bool
}

func profileForMode(mode string) (profile, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = scanModeQuick
	}

	quickCaps := []string{
		capGogoPortscan,
		capSprayCheck,
		capSprayFinger,
		capCoreWeb,
		capSprayCommon,
		capSprayBackup,
		capSprayActive,
		capSprayCrawl,
		capZombieWeakpass,
		capNeutronPOC,
	}

	switch mode {
	case scanModeQuick:
		return profile{
			Name:         scanModeQuick,
			Capabilities: capabilitySet(quickCaps...),
			CrawlDepth:   1,
		}, nil
	case scanModeFull:
		fullCaps := append([]string{}, quickCaps...)
		fullCaps = append(fullCaps,
			capSprayBrute,
		)
		return profile{
			Name:         scanModeFull,
			Capabilities: capabilitySet(fullCaps...),
			CrawlDepth:   2,
		}, nil
	default:
		return profile{}, fmt.Errorf("unknown scan mode %q, expected quick or full", mode)
	}
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
