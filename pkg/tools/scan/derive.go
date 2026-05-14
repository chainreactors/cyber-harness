package scan

import (
	"github.com/chainreactors/parsers"
	sdkzombie "github.com/chainreactors/sdk/zombie"
	"github.com/chainreactors/utils"
	zombiepkg "github.com/chainreactors/zombie/pkg"
)

func deriveServiceResult(profile profile, source string, result serviceResult, emit emitFunc) {
	if result.Result == nil {
		return
	}
	if source == "" {
		source = capGogoPortscan
	}

	fingers := parsers.FrameworkNames(result.Result.Frameworks)
	target := result.Result.GetTarget()
	if webURL := gogoWebURL(result.Result); webURL != "" {
		target = webURL
		emit(targetEvent(source, "", newWebTarget("", webURL, "")))
	}
	if len(fingers) > 0 {
		emit(findingEvent(source, fingerprintFinding{Target: target, Fingers: fingers}))
	}
	if len(fingers) > 0 || profile.AllowBroadPOC {
		emit(targetEvent(source, "", newPOCTarget("", target, fingers)))
	}
	if zTarget, ok := zombieTargetFromGogo(result.Result); ok {
		emit(targetEvent(source, "", newWeakpassTarget("", zTarget)))
	}
}

func deriveWebProbeResult(profile profile, result webProbeResult, emit emitFunc) {
	if !reportableSprayResult(result.Result) || result.Result.UrlString == "" {
		return
	}
	fingers := parsers.FrameworkNames(result.Result.Frameworks)
	if len(fingers) > 0 {
		emit(findingEvent(result.Source, fingerprintFinding{Target: result.Result.UrlString, Fingers: fingers}))
	}
	if result.Result.Status > 0 && (len(fingers) > 0 || profile.AllowBroadPOC) {
		emit(targetEvent(result.Source, "", newPOCTarget("", result.Result.UrlString, fingers)))
	}
	if result.Result.RedirectURL != "" {
		emit(targetEvent(result.Source+":redirect", "", newWebTarget("", result.Result.RedirectURL, result.HostHeader)))
	}
}

func deriveWeakpassResult(result weakpassResult, emit emitFunc) {
	if result.Result == nil {
		return
	}
	emit(findingEvent(result.Source, weakpassFinding{Result: result.Result}))
	if webURL := zombieResultWebURL(result.Result); webURL != "" {
		emit(targetEvent(result.Source, "", newWebTarget("", webURL, "")))
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

func gogoWebURL(result *parsers.GOGOResult) string {
	if result == nil || !result.IsHttp() {
		return ""
	}
	return result.GetBaseURL()
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

func webURLFromHostPort(host, port string) string {
	return utils.URLFromHostPort(webSchemeFromPort(port), host, port)
}

func webSchemeFromPort(port string) string {
	if port == "443" {
		return "https"
	}
	return "http"
}
