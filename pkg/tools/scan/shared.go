package scan

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/chainreactors/parsers"
	sdkzombie "github.com/chainreactors/sdk/zombie"
	zombiepkg "github.com/chainreactors/zombie/pkg"
)

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
