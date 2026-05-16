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

type scanOptions struct {
	Discovery   discoveryOptions
	Web         webOptions
	Credentials credentialOptions
}

type discoveryOptions struct {
	Ports    string
	Threads  int
	Timeout  int
	Version  int
	Exploit  string
	Debug    bool
	Explicit bool
}

type webOptions struct {
	Dictionaries []string
	Rules        []string
	Word         string
	DefaultDict  bool
	Advance      bool
}

type credentialOptions struct {
	Users     []string
	Passwords []string
}

func resolveScanOptions(flags flags) scanOptions {
	ports := defaultDiscoveryPorts(flags.Mode)
	explicitDiscovery := flags.Ports != "" || flags.Port != ""
	if flags.Ports != "" {
		ports = flags.Ports
	}
	if flags.Port != "" {
		ports = flags.Port
	}
	return scanOptions{
		Discovery: discoveryOptions{
			Ports:    ports,
			Threads:  flags.Threads,
			Timeout:  flags.Timeout,
			Version:  scanGogoVersionLevel,
			Exploit:  scanGogoExploitMode,
			Debug:    flags.Debug,
			Explicit: explicitDiscovery,
		},
		Web: webOptions{
			Dictionaries: append([]string(nil), flags.Dictionaries...),
			Rules:        append([]string(nil), flags.Rules...),
			Word:         flags.Word,
			DefaultDict:  flags.DefaultDict,
			Advance:      flags.Advance,
		},
		Credentials: credentialOptions{
			Users:     append([]string(nil), flags.Users...),
			Passwords: append([]string(nil), flags.Passwords...),
		},
	}
}

func defaultDiscoveryPorts(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), scanModeFull) {
		return scanFullDefaultPorts
	}
	return scanQuickDefaultPorts
}

func (o scanOptions) hasWeakpassOverrides() bool {
	return len(o.Credentials.Users) > 0 || len(o.Credentials.Passwords) > 0
}

func (o scanOptions) hasDiscoveryOverrides() bool {
	return o.Discovery.Explicit
}

func (o scanOptions) hasWebOverrides() bool {
	return len(o.Web.Dictionaries) > 0 || len(o.Web.Rules) > 0 || o.Web.Word != "" || o.Web.DefaultDict || o.Web.Advance
}

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
		capCoreWeb,
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
			capSprayPlugins,
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
