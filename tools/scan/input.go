package scan

import (
	"bufio"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	sdkzombie "github.com/chainreactors/sdk/zombie"
	"github.com/chainreactors/utils"
	"github.com/chainreactors/utils/parsers"
	zombiepkg "github.com/chainreactors/zombie/pkg"
)

const inputSource = "input"

func buildSeedEvents(rawInputs []string, onError func(string)) []event {
	var events []event
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
		for _, target := range parsed {
			events = append(events, targetEvent(inputSource, target))
		}
	}
	return events
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
		if _, _, err := net.ParseCIDR(raw); err == nil {
			return []target{newScanTarget(raw, "")}
		}
		return nil
	}
	if host, port, ok := utils.SplitHostPort(raw); ok {
		return seedTargetsFromHostPort(host, port)
	}
	return []target{newScanTarget(raw, "")}
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

func seedTargetsFromURL(raw string, parsed *url.URL) []target {
	var targets []target
	if utils.IsWebScheme(parsed.Scheme) {
		targets = append(targets, newWebTarget(raw, ""))
	}
	if target, ok := zombieTargetFromParsedURL(parsed); ok {
		if !isGenericWebZombieService(target.Service) {
			targets = append(targets, newWeakpassTarget(target))
		}
	}
	return targets
}

func seedTargetsFromHostPort(host, port string) []target {
	targets := []target{newScanTarget(host, port)}
	if utils.IsWebPort(port) {
		targets = append(targets, newWebTarget(utils.URLFromHostPort(webSchemeFromPort(port), host, port), ""))
		return targets
	}
	service := zombiepkg.GetDefault(port)
	if target, ok := normalizeZombieTarget(sdkzombie.Target{
		IP:      strings.TrimSpace(host),
		Port:    strings.TrimSpace(port),
		Service: service,
		Scheme:  service,
	}); ok {
		if !isGenericWebZombieService(target.Service) {
			targets = append(targets, newWeakpassTarget(target))
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

func zombieTargetFromParsedURL(parsed *url.URL) (sdkzombie.Target, bool) {
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
	return normalizeZombieTarget(target)
}

func normalizeZombieTarget(target sdkzombie.Target) (sdkzombie.Target, bool) {
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
