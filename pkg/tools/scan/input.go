package scan

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/chainreactors/utils"
)

const inputSource = "input"

func buildSeedEvents(rawInputs []string, sink eventSink) []event {
	return targetEvents(inputSource, buildSeedTargets(rawInputs, sink))
}

func buildSeedTargets(rawInputs []string, sink eventSink) []target {
	var targets []target
	for _, raw := range rawInputs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		parsed := seedTargetsFromInput(raw)
		if len(parsed) == 0 {
			if sink != nil {
				sink.Observe(pipelineEvent{Action: pipelineEventAccept, Event: errorEventOf("", fmt.Sprintf("skip invalid input: %s", raw))})
			}
			continue
		}
		targets = append(targets, parsed...)
	}
	return targets
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
	return seedTargetsFromHost(raw, raw)
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
		targets = append(targets, newWeakpassTarget(raw, target))
	}
	return targets
}

func seedTargetsFromHost(raw, host string) []target {
	return []target{newScanTarget(raw, host, "")}
}

func seedTargetsFromHostPort(host, port, raw string) []target {
	targets := []target{newScanTarget(raw, host, port)}
	if utils.IsWebPort(port) {
		targets = append(targets, newWebTarget(raw, webURLFromHostPort(host, port), ""))
	}
	if target, ok := zombieTargetFromHostPort(host, port, ""); ok {
		targets = append(targets, newWeakpassTarget(raw, target))
	}
	return targets
}
