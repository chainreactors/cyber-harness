package scan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chainreactors/parsers"
	sdkzombie "github.com/chainreactors/sdk/zombie"
	"github.com/chainreactors/utils"
)

type target interface {
	Kind() targetKind
	Key() string
	RawInput() string
}

type targetKind string

const (
	targetScan     targetKind = "scan-target"
	targetService  targetKind = "service-result"
	targetWeb      targetKind = "web-target"
	targetWebProbe targetKind = "web-probe-result"
	targetWeakpass targetKind = "weakpass-target"
	targetPOC      targetKind = "poc-target"
)

type scanTarget struct {
	Target string
	Ports  string
	Raw    string
}

func newScanTarget(raw, target, ports string) scanTarget {
	return scanTarget{
		Target: strings.TrimSpace(target),
		Ports:  strings.TrimSpace(ports),
		Raw:    strings.TrimSpace(raw),
	}
}

func (t scanTarget) Kind() targetKind { return targetScan }

func (t scanTarget) RawInput() string { return t.Raw }

func (t scanTarget) Key() string {
	return strings.ToLower(t.Target) + "|" + t.Ports
}

type serviceTarget struct {
	Result *parsers.GOGOResult
	Raw    string
}

func newServiceTarget(raw string, result *parsers.GOGOResult) serviceTarget {
	return serviceTarget{
		Result: result,
		Raw:    strings.TrimSpace(raw),
	}
}

func (t serviceTarget) Kind() targetKind { return targetService }

func (t serviceTarget) RawInput() string { return t.Raw }

func (t serviceTarget) Key() string {
	if t.Result == nil {
		return ""
	}
	return fmt.Sprintf("%s|%s|%s", strings.ToLower(t.Result.Ip), t.Result.Port, t.Result.Protocol)
}

type webTarget struct {
	URL        string
	HostHeader string
	Raw        string
}

func newWebTarget(raw, rawURL, hostHeader string) webTarget {
	return webTarget{
		URL:        strings.TrimSpace(rawURL),
		HostHeader: strings.ToLower(strings.TrimSpace(hostHeader)),
		Raw:        strings.TrimSpace(raw),
	}
}

func (t webTarget) Kind() targetKind { return targetWeb }

func (t webTarget) RawInput() string { return t.Raw }

func (t webTarget) Key() string {
	return utils.NormalizeURL(t.URL) + "|host=" + strings.ToLower(t.HostHeader)
}

type webProbeTarget struct {
	Result     *parsers.SprayResult
	HostHeader string
	Capability string
	Raw        string
}

func newWebProbeTarget(raw, capability, hostHeader string, result *parsers.SprayResult) webProbeTarget {
	return webProbeTarget{
		Result:     result,
		HostHeader: strings.ToLower(strings.TrimSpace(hostHeader)),
		Capability: strings.TrimSpace(capability),
		Raw:        strings.TrimSpace(raw),
	}
}

func (t webProbeTarget) Kind() targetKind { return targetWebProbe }

func (t webProbeTarget) RawInput() string { return t.Raw }

func (t webProbeTarget) Key() string {
	if t.Result == nil {
		return ""
	}
	return fmt.Sprintf("%s|%s|%s|%s|%d|%d",
		t.Capability,
		utils.NormalizeURL(t.Result.UrlString),
		strings.ToLower(t.HostHeader),
		t.Result.Path,
		t.Result.Status,
		t.Result.Source,
	)
}

type weakpassTarget struct {
	Target sdkzombie.Target
	Raw    string
}

func newWeakpassTarget(raw string, target sdkzombie.Target) weakpassTarget {
	target.Service = strings.ToLower(strings.TrimSpace(target.Service))
	target.Scheme = strings.ToLower(strings.TrimSpace(target.Scheme))
	target.IP = strings.TrimSpace(target.IP)
	target.Port = strings.TrimSpace(target.Port)
	return weakpassTarget{
		Target: target,
		Raw:    strings.TrimSpace(raw),
	}
}

func (t weakpassTarget) Kind() targetKind { return targetWeakpass }

func (t weakpassTarget) RawInput() string { return t.Raw }

func (t weakpassTarget) Key() string {
	target := t.Target
	key := strings.ToLower(target.Service) + "://" + strings.ToLower(target.Address())
	if target.Username != "" || target.Password != "" {
		key += "|" + target.Username + "|" + target.Password
	}
	if len(target.Param) > 0 {
		keys := make([]string, 0, len(target.Param))
		for k, v := range target.Param {
			if k != "" && v != "" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			key += "|" + strings.ToLower(k) + "=" + target.Param[k]
		}
	}
	return key
}

type pocTarget struct {
	Target  string
	Fingers []string
	Raw     string
}

func newPOCTarget(raw, target string, fingers []string) pocTarget {
	return pocTarget{
		Target:  strings.TrimSpace(target),
		Fingers: parsers.NormalizeNames(fingers),
		Raw:     strings.TrimSpace(raw),
	}
}

func (t pocTarget) Kind() targetKind { return targetPOC }

func (t pocTarget) RawInput() string { return t.Raw }

func (t pocTarget) Key() string {
	return strings.ToLower(t.Target) + "|" + strings.Join(parsers.NormalizeNames(t.Fingers), ",")
}
