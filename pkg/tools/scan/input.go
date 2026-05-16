package scan

import (
	"bufio"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/chainreactors/parsers"
	sdkzombie "github.com/chainreactors/sdk/zombie"
	"github.com/chainreactors/utils"
	zombiepkg "github.com/chainreactors/zombie/pkg"
)

const inputSource = "input"

func buildSeedEvents(rawInputs []string, onError func(string)) []event {
	var targets []target
	for _, raw := range rawInputs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		parsed := seedTargetsFromInput(raw)
		if len(parsed) == 0 {
			if onError != nil {
				onError(raw)
			}
			continue
		}
		targets = append(targets, parsed...)
	}
	return targetEvents(inputSource, targets)
}

func seedTargetsFromInput(raw string) []target {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	if parsed, ok := parseInputURL(raw); ok {
		return seedTargetsFromURL(raw, parsed)
	}
	if strings.Contains(raw, "://") {
		return nil
	}
	if strings.Contains(raw, "/") {
		if isCIDRInput(raw) {
			return []target{newScanTarget(raw, raw, "")}
		}
		return nil
	}
	if host, port, ok := utils.SplitHostPort(raw); ok {
		return seedTargetsFromHostPort(host, port, raw)
	}
	return []target{newScanTarget(raw, raw, "")}
}

func targetEvents(source string, targets []target) []event {
	if len(targets) == 0 {
		return nil
	}
	events := make([]event, 0, len(targets))
	for _, target := range targets {
		if target == nil {
			continue
		}
		events = append(events, targetEvent(source, "", target))
	}
	return events
}

func parseInputURL(raw string) (*url.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.Contains(raw, "://") {
		return nil, false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return nil, false
	}
	return parsed, true
}

func isCIDRInput(raw string) bool {
	_, _, err := net.ParseCIDR(strings.TrimSpace(raw))
	return err == nil
}

func seedTargetsFromURL(raw string, parsed *url.URL) []target {
	if parsed == nil {
		return nil
	}
	var targets []target
	if utils.IsWebScheme(parsed.Scheme) {
		targets = append(targets, newWebTarget(raw, raw, ""))
	}
	if target, ok := zombieTargetFromParsedURL(parsed, ""); ok {
		if !isGenericWebZombieService(target.Service) {
			targets = append(targets, newWeakpassTarget(raw, target))
		}
	}
	return targets
}

func seedTargetsFromHostPort(host, port, raw string) []target {
	targets := []target{newScanTarget(raw, host, port)}
	if utils.IsWebPort(port) {
		targets = append(targets, newWebTarget(raw, utils.URLFromHostPort(webSchemeFromPort(port), host, port), ""))
		return targets
	}
	if target, ok := zombieTargetFromHostPort(host, port, ""); ok {
		if !isGenericWebZombieService(target.Service) {
			targets = append(targets, newWeakpassTarget(raw, target))
		}
	}
	return targets
}

func readInputs(inputs []string, listFile string) ([]string, error) {
	var out []string
	for _, input := range inputs {
		input = strings.TrimSpace(input)
		if input != "" {
			out = append(out, input)
		}
	}
	if listFile == "" {
		return out, nil
	}

	f, err := os.Open(listFile)
	if err != nil {
		return nil, fmt.Errorf("open input list: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, scanner.Err()
}

func zombieTargetFromParsedURL(parsed *url.URL, serviceOverride string) (sdkzombie.Target, bool) {
	if parsed == nil || parsed.Hostname() == "" {
		return sdkzombie.Target{}, false
	}
	target := sdkzombie.Target{
		IP:      parsed.Hostname(),
		Port:    parsed.Port(),
		Scheme:  parsed.Scheme,
		Service: parsed.Scheme,
	}
	if service, ok := parsers.ZombieServiceFromName(parsed.Scheme); ok {
		target.Service = service
	}
	if parsed.User != nil {
		target.Username = parsed.User.Username()
		target.Password, _ = parsed.User.Password()
	}
	return normalizeZombieTarget(target, serviceOverride)
}

func zombieTargetFromHostPort(host, port, serviceOverride string) (sdkzombie.Target, bool) {
	service := zombiepkg.GetDefault(port)
	return normalizeZombieTarget(sdkzombie.Target{
		IP:      strings.TrimSpace(host),
		Port:    strings.TrimSpace(port),
		Service: service,
		Scheme:  service,
	}, serviceOverride)
}

func normalizeZombieTarget(target sdkzombie.Target, serviceOverride string) (sdkzombie.Target, bool) {
	if serviceOverride != "" {
		service := strings.ToLower(serviceOverride)
		if mapped, ok := parsers.ZombieServiceFromName(service); ok {
			service = mapped
		}
		target.Service = service
		target.Scheme = target.Service
	}
	if target.Port == "" && target.Service != "" {
		target.Port = zombiepkg.Services.DefaultPort(target.Service)
	}
	if target.Service == "" || target.Service == "unknown" {
		return sdkzombie.Target{}, false
	}
	return target, true
}

func isGenericWebZombieService(service string) bool {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case "http", "https", "get", "post":
		return true
	default:
		return false
	}
}
