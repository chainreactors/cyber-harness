package scan

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/parsers"
	sdkzombie "github.com/chainreactors/sdk/zombie"
)

func (c *Command) runPortDiscoveryCapability(ctx context.Context, discovery discoveryOptions, profile profile, input target, emit emitFunc) {
	target, ok := input.(scanTarget)
	if !ok {
		return
	}
	ports := discovery.Ports
	if target.Ports != "" {
		ports = target.Ports
	}
	c.logger.Infof("scan %s %s %s", capGogoPortscan, target.Target, ports)
	resultCh, err := gogoScanStream(ctx, c.engines.Gogo, gogoScanOptions{
		Target:       target.Target,
		Ports:        ports,
		Threads:      discovery.Threads,
		Timeout:      discovery.Timeout,
		VersionLevel: discovery.Version,
	})
	if err != nil {
		emit(errorEventOf(capGogoPortscan, fmt.Sprintf("gogo %s: %v", target.Target, err)))
		return
	}
	for result := range resultCh {
		if ctx.Err() != nil {
			return
		}
		if result == nil {
			continue
		}
		emit(targetEvent(capGogoPortscan, target.Raw, newServiceTarget(target.Raw, result)))
		deriveServiceResult(profile, capGogoPortscan, serviceResult{Result: result}, emit)
	}
}

func (c *Command) runSprayCapability(ctx context.Context, flags flags, web webOptions, input target, source string, opts sprayCheckOptions, emit emitFunc) {
	target, ok := input.(webTarget)
	if !ok || target.URL == "" {
		return
	}
	opts = applyWebStrategyOptions(flags, web, opts)
	opts.URLs = []string{target.URL}
	opts.Host = target.HostHeader

	resultCh, err := sprayCheckStream(ctx, c.engines.Spray, opts)
	if err != nil {
		emit(errorEventOf(source, fmt.Sprintf("spray %s: %v", target.URL, err)))
		return
	}
	for result := range resultCh {
		if ctx.Err() != nil {
			return
		}
		if !reportableSprayResult(result) {
			continue
		}
		emit(targetEvent(source, target.Raw, newWebProbeTarget(target.Raw, source, target.HostHeader, result)))
	}
}

func applyWebStrategyOptions(flags flags, web webOptions, opts sprayCheckOptions) sprayCheckOptions {
	opts.Dictionaries = append([]string(nil), web.Dictionaries...)
	opts.Rules = append([]string(nil), web.Rules...)
	opts.Word = web.Word
	opts.DefaultDict = opts.DefaultDict || web.DefaultDict
	opts.Advance = opts.Advance || web.Advance
	opts.ReconPlugin = true
	opts.Threads = flags.SprayThreads
	opts.Timeout = flags.Timeout
	return opts
}

func runWebResultAnalysisCapability(_ context.Context, profile profile, input target, emit emitFunc) {
	target, ok := input.(webProbeTarget)
	if !ok || !reportableSprayResult(target.Result) {
		return
	}
	deriveWebProbeResult(profile, webProbeResult{Source: target.Capability, Result: target.Result, HostHeader: target.HostHeader}, emit)
}

func (c *Command) runWeakpassCapability(ctx context.Context, flags flags, credentials credentialOptions, input target, emit emitFunc) {
	target, ok := input.(weakpassTarget)
	if !ok || target.Target.Service == "" || target.Target.Address() == ":" {
		return
	}

	resultCh, err := zombieWeakpassStream(ctx, c.engines.Zombie, zombieWeakpassOptions{
		Targets:   []sdkzombie.Target{target.Target},
		Threads:   flags.ZombieThreads,
		Timeout:   flags.Timeout,
		Top:       flags.ZombieTop,
		Users:     credentials.Users,
		Passwords: credentials.Passwords,
	})
	if err != nil {
		emit(errorEventOf(capZombieWeakpass, fmt.Sprintf("zombie %s: %v", target.Target.Address(), err)))
		return
	}
	for result := range resultCh {
		if ctx.Err() != nil {
			return
		}
		if result == nil {
			continue
		}
		deriveWeakpassResult(weakpassResult{Source: capZombieWeakpass, Result: result}, emit)
	}
}

func (c *Command) runPOCCapability(ctx context.Context, flags flags, input target, emit emitFunc) {
	target, ok := input.(pocTarget)
	if !ok || target.Target == "" {
		return
	}
	fingers := parsers.NormalizeNames(target.Fingers)
	if len(fingers) == 0 && !flags.BroadPOC {
		return
	}
	resultCh, err := neutronExecuteStream(ctx, c.engines.Neutron, c.engines.Index, neutronExecuteOptions{
		Target:       target.Target,
		Fingers:      fingers,
		MaxPerFinger: flags.MaxNeutronPerFP,
		Broad:        flags.BroadPOC,
	})
	if err == errNoNeutronTemplates {
		return
	}
	if err != nil {
		emit(errorEventOf(capNeutronPOC, fmt.Sprintf("neutron %s: %v", target.Target, err)))
		return
	}
	for result := range resultCh {
		if ctx.Err() != nil {
			return
		}
		if result == nil || !result.Matched() {
			continue
		}
		tmpl := result.Template()
		templateID := ""
		severity := ""
		name := ""
		if tmpl != nil {
			templateID = tmpl.Id
			severity = tmpl.Info.Severity
			name = tmpl.Info.Name
		}
		fields := []string{"[vuln]", target.Target}
		fields = appendNonEmptyValue(fields, templateID)
		fields = appendNonEmptyValue(fields, severity)
		fields = appendNonEmptyValue(fields, name)
		emit(findingEvent(capNeutronPOC, vulnFinding{
			Message: strings.Join(fields, " "),
		}))
	}
}
