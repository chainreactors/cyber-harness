package scan

import (
	"context"
	"net"
	"net/url"
	"strings"

	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/tools/scan/engine"
	"github.com/chainreactors/cyber/tools/toolargs"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
	sdkzombie "github.com/chainreactors/sdk/zombie"
	"github.com/chainreactors/utils"
	"github.com/chainreactors/utils/parsers"
	zombiepkg "github.com/chainreactors/zombie/pkg"
)

func (c *Command) runPortDiscoveryCapability(ctx context.Context, flags flags, input target, emit func(event)) {
	target, ok := input.(scanTarget)
	if !ok {
		return
	}
	ports := flags.Ports
	if ports == "" {
		ports = defaultDiscoveryPorts(flags.Mode)
	}
	if target.Ports != "" {
		ports = target.Ports
	}
	c.Logger.Infof("scan capability=%s target=%s ports=%s", capGogoPortscan, target.Target, ports)
	resultCh, err := engine.GogoScanStream(ctx, c.engines.Gogo, engine.GogoScanOptions{
		Target:       target.Target,
		Ports:        ports,
		Threads:      flags.Threads,
		Timeout:      flags.Timeout,
		VersionLevel: scanGogoVersionLevel,
		Exploit:      scanGogoExploitMode,
		Proxy:        c.proxyForContext(ctx),
		Debug:        flags.Debug,
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
		emit(targetEvent(capGogoPortscan, serviceTarget{Result: result}))
		deriveServiceResult(flags.BroadPOC, capGogoPortscan, result, emit)
	}
}

func (c *Command) runSprayCapability(ctx context.Context, flags flags, input target, source string, opts engine.SprayCheckOptions, emit func(event)) {
	target, ok := input.(webTarget)
	if !ok || target.URL == "" {
		return
	}
	opts = applyWebStrategyOptions(flags, opts)
	opts.Proxy = c.proxyForContext(ctx)
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
		emit(targetEvent(source, newWebProbeTarget(target.HostHeader, result)))
	}
}

func applyWebStrategyOptions(flags flags, opts engine.SprayCheckOptions) engine.SprayCheckOptions {
	opts.Dictionaries = append([]string(nil), flags.Dictionaries...)
	opts.Rules = append([]string(nil), flags.Rules...)
	opts.Word = flags.Word
	opts.DefaultDict = opts.DefaultDict || flags.DefaultDict
	opts.Advance = opts.Advance || flags.Advance
	opts.ReconPlugin = true
	opts.Threads = flags.SprayThreads
	opts.Timeout = flags.Timeout
	opts.Debug = flags.Debug
	opts.Quiet = flags.JSON
	return opts
}

func (c *Command) runWeakpassCapability(ctx context.Context, flags flags, input target, emit func(event)) {
	target, ok := input.(weakpassTarget)
	if !ok || target.Target.Service == "" || target.Target.Address() == ":" {
		return
	}

	resultCh, err := engine.ZombieWeakpassStream(ctx, c.engines.Zombie, engine.ZombieWeakpassOptions{
		Targets:   []sdkzombie.Target{target.Target},
		Threads:   flags.ZombieThreads,
		Timeout:   flags.Timeout,
		Top:       flags.ZombieTop,
		Users:     append([]string(nil), flags.Users...),
		Passwords: append([]string(nil), flags.Passwords...),
		Proxy:     c.proxyForContext(ctx),
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
		record := result.TemplateResult(target.Target)
		resultID := toolargs.ArtifactResultID("neutron", toolpb.ArtifactKindVuln, target.Target, record)
		loot := bindLoot(vulnLoot(record), resultID, "neutron")
		emit(artifactLootEvent(capNeutronPOC, loot, artifactResult{
			ResultID: resultID,
			Tool:     "neutron",
			Kind:     toolpb.ArtifactKindVuln,
			Target:   target.Target,
			Data:     record,
		}))
	}
}

func deriveServiceResult(broadPOC bool, source string, result *parsers.GOGOResult, emit func(event)) {
	if result == nil {
		return
	}
	fingers := parsers.FrameworkNames(result.Frameworks)
	target := result.GetTarget()
	if result.IsHttp() {
		target = result.GetBaseURL()
		emit(targetEvent(source, newWebTarget(target, "")))
	}
	if len(fingers) > 0 {
		resultID := toolargs.ArtifactResultID("gogo", toolpb.ArtifactKindService, result.GetTarget(), result)
		emit(lootEvent(source, bindLoot(
			fingerprintLoot(target, parsers.NormalizeNames(fingers), result.Frameworks.IsFocus()),
			resultID,
			"gogo",
		)))
	}
	if len(fingers) > 0 || broadPOC {
		emit(targetEvent(source, newPOCTarget(target, fingers)))
	}
	if zTarget, ok := zombieTargetFromGogo(result); ok {
		emit(targetEvent(source, newWeakpassTarget(zTarget)))
	}
}

func (c *Command) runHTTPBasicAuthCapability(ctx context.Context, flags flags, input event, emit func(event)) {
	target, ok := input.Target.(webProbeTarget)
	if !ok || !reportableSprayResultForCapability(target.Result, input.Source) || target.Result.Status != 401 {
		return
	}
	zTarget, ok := basicAuthZombieTarget(ctx, target.Result.UrlString, target.HostHeader, flags.Timeout, c.proxyForContext(ctx))
	if !ok {
		return
	}
	emit(targetEvent(capHTTPBasicAuth, newWeakpassTarget(zTarget)))
}

func deriveWebProbeResult(broadPOC bool, source string, result *parsers.SprayResult, emit func(event)) {
	if !reportableSprayResult(result) || result.UrlString == "" {
		return
	}
	fingers := parsers.FrameworkNames(result.Frameworks)
	if len(fingers) > 0 {
		resultID := toolargs.ArtifactResultID("spray", toolpb.ArtifactKindWeb, result.UrlString, result)
		emit(lootEvent(source, bindLoot(
			fingerprintLoot(result.UrlString, parsers.NormalizeNames(fingers), result.Frameworks.IsFocus()),
			resultID,
			"spray",
		)))
	}
	if result.Status > 0 && (len(fingers) > 0 || broadPOC) {
		emit(targetEvent(source, newPOCTarget(result.UrlString, fingers)))
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
	target := result.Address()
	resultID := toolargs.ArtifactResultID("zombie", toolpb.ArtifactKindWeakpass, target, result)
	loot := bindLoot(weakpassLoot(result), resultID, "zombie")
	emit(artifactLootEvent(source, loot, artifactResult{
		ResultID: resultID,
		Tool:     "zombie",
		Kind:     toolpb.ArtifactKindWeakpass,
		Target:   target,
		Data:     result,
	}))
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
