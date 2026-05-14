package scan

import (
	"context"
	"strings"

	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/parsers"
	sdkzombie "github.com/chainreactors/sdk/zombie"
	"github.com/chainreactors/utils"
	zombiepkg "github.com/chainreactors/zombie/pkg"
)

func (c *Command) runPortDiscoveryCapability(ctx context.Context, discovery discoveryOptions, profile profile, input target, emit func(event)) {
	target, ok := input.(scanTarget)
	if !ok {
		return
	}
	ports := discovery.Ports
	if target.Ports != "" {
		ports = target.Ports
	}
	c.logger.Infof("scan %s %s %s", capGogoPortscan, target.Target, ports)
	resultCh, err := engine.GogoScanStream(ctx, c.engines.Gogo, engine.GogoScanOptions{
		Target:       target.Target,
		Ports:        ports,
		Threads:      discovery.Threads,
		Timeout:      discovery.Timeout,
		VersionLevel: discovery.Version,
	})
	if err != nil {
		emitError(emit, capGogoPortscan, "gogo %s: %v", target.Target, err)
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
		deriveServiceResult(profile, capGogoPortscan, result, emit)
	}
}

func (c *Command) runSprayCapability(ctx context.Context, flags flags, web webOptions, input target, source string, opts engine.SprayCheckOptions, emit func(event)) {
	target, ok := input.(webTarget)
	if !ok || target.URL == "" {
		return
	}
	opts = applyWebStrategyOptions(flags, web, opts)
	opts.URLs = []string{target.URL}
	opts.Host = target.HostHeader

	resultCh, err := engine.SprayCheckStream(ctx, c.engines.Spray, opts)
	if err != nil {
		emitError(emit, source, "spray %s: %v", target.URL, err)
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

func applyWebStrategyOptions(flags flags, web webOptions, opts engine.SprayCheckOptions) engine.SprayCheckOptions {
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

func runWebResultAnalysisCapability(_ context.Context, profile profile, input target, emit func(event)) {
	target, ok := input.(webProbeTarget)
	if !ok || !reportableSprayResult(target.Result) {
		return
	}
	deriveWebProbeResult(profile, target.Capability, target.Result, target.HostHeader, emit)
}

func (c *Command) runWeakpassCapability(ctx context.Context, flags flags, credentials credentialOptions, input target, emit func(event)) {
	target, ok := input.(weakpassTarget)
	if !ok || target.Target.Service == "" || target.Target.Address() == ":" {
		return
	}

	resultCh, err := engine.ZombieWeakpassStream(ctx, c.engines.Zombie, engine.ZombieWeakpassOptions{
		Targets:   []sdkzombie.Target{target.Target},
		Threads:   flags.ZombieThreads,
		Timeout:   flags.Timeout,
		Top:       flags.ZombieTop,
		Users:     credentials.Users,
		Passwords: credentials.Passwords,
	})
	if err != nil {
		emitError(emit, capZombieWeakpass, "zombie %s: %v", target.Target.Address(), err)
		return
	}
	for result := range resultCh {
		if ctx.Err() != nil {
			return
		}
		if result == nil {
			continue
		}
		deriveWeakpassResult(capZombieWeakpass, result, emit)
	}
}

func (c *Command) runPOCCapability(ctx context.Context, flags flags, input target, emit func(event)) {
	target, ok := input.(pocTarget)
	if !ok || target.Target == "" {
		return
	}
	fingers := parsers.NormalizeNames(target.Fingers)
	if len(fingers) == 0 && !flags.BroadPOC {
		return
	}
	resultCh, err := engine.NeutronExecuteStream(ctx, c.engines.Neutron, c.engines.Index, engine.NeutronExecuteOptions{
		Target:       target.Target,
		Fingers:      fingers,
		MaxPerFinger: flags.MaxNeutronPerFP,
		Broad:        flags.BroadPOC,
	})
	if err == engine.ErrNoNeutronTemplates {
		return
	}
	if err != nil {
		emitError(emit, capNeutronPOC, "neutron %s: %v", target.Target, err)
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

func deriveServiceResult(profile profile, source string, result *parsers.GOGOResult, emit func(event)) {
	if result == nil {
		return
	}
	if source == "" {
		source = capGogoPortscan
	}

	fingers := parsers.FrameworkNames(result.Frameworks)
	target := result.GetTarget()
	if result.IsHttp() {
		target = result.GetBaseURL()
		emit(targetEvent(source, "", newWebTarget("", target, "")))
	}
	if len(fingers) > 0 {
		emit(findingEvent(source, fingerprintFinding{Target: target, Fingers: fingers}))
	}
	if len(fingers) > 0 || profile.AllowBroadPOC {
		emit(targetEvent(source, "", newPOCTarget("", target, fingers)))
	}
	if zTarget, ok := zombieTargetFromGogo(result); ok {
		emit(targetEvent(source, "", newWeakpassTarget("", zTarget)))
	}
}

func deriveWebProbeResult(profile profile, source string, result *parsers.SprayResult, hostHeader string, emit func(event)) {
	if !reportableSprayResult(result) || result.UrlString == "" {
		return
	}
	fingers := parsers.FrameworkNames(result.Frameworks)
	if len(fingers) > 0 {
		emit(findingEvent(source, fingerprintFinding{Target: result.UrlString, Fingers: fingers}))
	}
	if result.Status > 0 && (len(fingers) > 0 || profile.AllowBroadPOC) {
		emit(targetEvent(source, "", newPOCTarget("", result.UrlString, fingers)))
	}
	if result.RedirectURL != "" {
		emit(targetEvent(source+":redirect", "", newWebTarget("", result.RedirectURL, hostHeader)))
	}
}

func deriveWeakpassResult(source string, result *parsers.ZombieResult, emit func(event)) {
	if result == nil {
		return
	}
	emit(findingEvent(source, weakpassFinding{Result: result}))
	if webURL := zombieResultWebURL(result); webURL != "" {
		emit(targetEvent(source, "", newWebTarget("", webURL, "")))
	}
}

func zombieTargetFromGogo(result *parsers.GOGOResult) (sdkzombie.Target, bool) {
	service, ok := gogoZombieService(result)
	if !ok || service == "" || service == "unknown" || utils.IsWebPort(result.Port) {
		return sdkzombie.Target{}, false
	}
	return sdkzombie.Target{
		IP:      result.Ip,
		Port:    result.Port,
		Service: service,
		Scheme:  service,
	}, true
}

func gogoZombieService(result *parsers.GOGOResult) (string, bool) {
	if result == nil {
		return "", false
	}
	for _, name := range parsers.FrameworkNames(result.Frameworks) {
		if service, ok := parsers.ZombieServiceFromName(name); ok {
			return service, true
		}
	}
	for _, vuln := range result.Vulns {
		if vuln == nil {
			continue
		}
		for _, tag := range vuln.Tags {
			if service, ok := parsers.ZombieServiceFromName(tag); ok {
				return service, true
			}
		}
	}
	service := zombiepkg.GetDefault(result.Port)
	if service == "" || service == "unknown" {
		return "", false
	}
	return service, true
}

func zombieResultWebURL(result *parsers.ZombieResult) string {
	if result == nil {
		return ""
	}
	if result.Service != "http" && result.Service != "https" && !utils.IsWebPort(result.Port) {
		return ""
	}
	scheme := result.Scheme
	if scheme == "" {
		scheme = result.Service
	}
	if scheme == "" || scheme == "unknown" {
		scheme = webSchemeFromPort(result.Port)
	}
	return utils.URLFromHostPort(scheme, result.IP, result.Port)
}

func webSchemeFromPort(port string) string {
	if port == "443" {
		return "https"
	}
	return "http"
}
