package scan

import (
	"context"
	"net"
	"net/url"
	"strings"

	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/parsers"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
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
	c.logger.Infof("scan capability=%s target=%s ports=%s", capGogoPortscan, target.Target, ports)
	resultCh, err := engine.GogoScanStream(ctx, c.engines.Gogo, engine.GogoScanOptions{
		Target:       target.Target,
		Ports:        ports,
		Threads:      discovery.Threads,
		Timeout:      discovery.Timeout,
		VersionLevel: discovery.Version,
		Exploit:      discovery.Exploit,
		Proxy:        c.proxy,
		Debug:        discovery.Debug,
		OnStats: func(stats sdktypes.Stats) {
			emit(statsEvent(capGogoPortscan, stats))
		},
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
	opts.Scope = webTargetScope(target)
	opts.OnStats = func(stats sdktypes.Stats) {
		emit(statsEvent(source, stats))
	}

	resultCh, err := engine.SprayCheckStream(ctx, c.engines.Spray, opts)
	if err != nil {
		emitError(emit, source, "spray %s: %v", target.URL, err)
		return
	}
	for result := range resultCh {
		if ctx.Err() != nil {
			return
		}
		if !reportableSprayResultForCapability(result, source) {
			continue
		}
		result = sanitizeSprayResultScope(target.URL, result)
		if result == nil {
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
	opts.Debug = flags.Debug
	return opts
}

func runWebResultAnalysisCapability(_ context.Context, profile profile, input target, emit func(event)) {
	target, ok := input.(webProbeTarget)
	if !ok || !reportableSprayResultForCapability(target.Result, target.Capability) {
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
		Proxy:     c.proxy,
		Debug:     flags.Debug,
		OnStats: func(stats sdktypes.Stats) {
			emit(statsEvent(capZombieWeakpass, stats))
		},
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
	if len(target.Fingers) == 0 && !flags.BroadPOC {
		return
	}
	resultCh, err := engine.NeutronExecuteStream(ctx, c.engines.Neutron, c.engines.Index, engine.NeutronExecuteOptions{
		Target:       target.Target,
		Fingers:      target.Fingers,
		MaxPerFinger: flags.MaxNeutronPerFP,
		Broad:        flags.BroadPOC,
		Debug:        flags.Debug,
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
		emit(lootEvent(capNeutronPOC, vulnLoot(result.VulnResult(target.Target))))
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
		emit(lootEvent(source, fingerprintLoot(target, parsers.NormalizeNames(fingers), result.Frameworks.IsFocus())))
	}
	if len(fingers) > 0 || profile.AllowBroadPOC {
		emit(targetEvent(source, "", newPOCTarget("", target, fingers)))
	}
	if zTarget, ok := zombieTargetFromGogo(result); ok {
		emit(targetEvent(source, "", newWeakpassTarget("", zTarget)))
	}
}

func (c *Command) runHTTPBasicAuthCapability(ctx context.Context, flags flags, input target, emit func(event)) {
	target, ok := input.(webProbeTarget)
	if !ok || !reportableSprayResultForCapability(target.Result, target.Capability) || target.Result.Status != 401 {
		return
	}
	zTarget, ok := basicAuthZombieTarget(ctx, target.Result.UrlString, target.HostHeader, flags.Timeout)
	if !ok {
		return
	}
	emit(targetEvent(capHTTPBasicAuth, target.Raw, newWeakpassTarget(target.Raw, zTarget)))
}

func deriveWebProbeResult(profile profile, source string, result *parsers.SprayResult, hostHeader string, emit func(event)) {
	if !reportableSprayResult(result) || result.UrlString == "" {
		return
	}
	fingers := parsers.FrameworkNames(result.Frameworks)
	if len(fingers) > 0 {
		emit(lootEvent(source, fingerprintLoot(result.UrlString, parsers.NormalizeNames(fingers), result.Frameworks.IsFocus())))
	}
	if result.Status > 0 && (len(fingers) > 0 || profile.AllowBroadPOC) {
		emit(targetEvent(source, "", newPOCTarget("", result.UrlString, fingers)))
	}
}

func webTargetScope(target webTarget) []string {
	base, err := url.Parse(strings.TrimSpace(target.URL))
	if err != nil || base.Host == "" {
		return nil
	}
	scope := []string{strings.ToLower(base.Host)}
	if target.HostHeader != "" {
		scope = append(scope, strings.ToLower(target.HostHeader))
		if _, _, err := net.SplitHostPort(target.HostHeader); err != nil && base.Port() != "" {
			scope = append(scope, strings.ToLower(net.JoinHostPort(target.HostHeader, base.Port())))
		}
	}
	return uniqueStrings(scope)
}

func sanitizeSprayResultScope(baseURL string, result *parsers.SprayResult) *parsers.SprayResult {
	if result == nil {
		return nil
	}
	if !sameAssetURL(baseURL, result.UrlString) {
		return nil
	}
	if result.RedirectURL == "" || sameAssetURL(baseURL, result.RedirectURL) {
		return result
	}
	clone := *result
	clone.RedirectURL = ""
	return &clone
}

func sameAssetURL(baseURL, candidate string) bool {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Host == "" {
		return true
	}
	ref, err := url.Parse(strings.TrimSpace(candidate))
	if err != nil || ref.Host == "" {
		return true
	}
	return strings.EqualFold(base.Hostname(), ref.Hostname()) && effectivePort(base) == effectivePort(ref)
}

func effectivePort(u *url.URL) string {
	if u == nil {
		return ""
	}
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func deriveWeakpassResult(source string, result *parsers.ZombieResult, emit func(event)) {
	if result == nil {
		return
	}
	emit(lootEvent(source, weakpassLoot(result)))
}

func zombieTargetFromGogo(result *parsers.GOGOResult) (sdkzombie.Target, bool) {
	service, ok := gogoZombieService(result)
	if !ok || service == "" || service == "unknown" || result.IsHttp() || isGenericWebZombieService(service) || utils.IsWebPort(result.Port) {
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

func webSchemeFromPort(port string) string {
	if port == "443" {
		return "https"
	}
	return "http"
}
