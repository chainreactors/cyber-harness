package cmd

import (
	"fmt"
	"strings"

	cyberhubcmd "github.com/chainreactors/aiscan/pkg/tools/cyberhub"
	"github.com/chainreactors/aiscan/pkg/tools/scan"
)

func isScannerHelpRequest(args []string) bool {
	if len(args) < 2 {
		return false
	}
	for _, arg := range args[1:] {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func staticScannerUsage(name string) (string, bool) {
	switch name {
	case "scan":
		return scan.Usage(), true
	case "cyberhub":
		return cyberhubcmd.Usage(), true
	case "gogo":
		return "gogo - host, port, service, and banner discovery\nUsage: gogo [options]\n", true
	case "spray":
		return "spray - web probing, fingerprints, common files, and crawl checks\nUsage: spray [options]\n", true
	case "zombie":
		return "zombie - weak credential checks for supported services\nUsage: zombie [options]\n", true
	case "neutron":
		return "neutron - POC/vulnerability testing with nuclei-style options\nUsage: neutron -u <target> [options]\n", true
	default:
		return "", false
	}
}

func directScannerRuntimeFeatures(rest []string) (runtimeFeatures, []string, error) {
	if len(rest) == 0 {
		return runtimeFeatures{}, nil, fmt.Errorf("missing scanner command")
	}
	if rest[0] != "scan" {
		return runtimeFeatures{}, rest, nil
	}
	verifyMode, explicit := scannerVerifyMode(rest[1:])
	sniperEnabled := hasScannerFlag(rest[1:], "--sniper")
	warning := ""
	if explicit {
		warning = "--verify is deprecated; use --ai"
	}

	features := runtimeFeatures{}

	if sniperEnabled {
		features.ProviderEnabled = true
		features.ProviderOptional = false
		features.AIEnabled = true
	}

	switch verifyMode {
	case "auto":
		features.ProviderEnabled = true
		if !sniperEnabled {
			features.ProviderOptional = true
		}
		features.AIEnabled = true
		features.ScannerAI = explicit
		features.Warning = warning
		return features, removeScannerFlag(rest, "--verify"), nil
	case "off":
		if explicit {
			features.Warning = warning
			return features, replaceOrAppendScannerFlag(rest, "--verify", "off"), nil
		}
		return features, rest, nil
	case "low", "medium", "high", "critical":
		features.ProviderEnabled = true
		if !sniperEnabled {
			features.ProviderOptional = !explicit
		}
		features.AIEnabled = true
		features.ScannerAI = explicit
		features.Warning = warning
		return features, rest, nil
	default:
		if explicit {
			return runtimeFeatures{}, nil, fmt.Errorf("invalid --verify value %q: expected auto, off, low, medium, high, or critical", verifyMode)
		}
		return features, rest, nil
	}
}

func scannerVerifyMode(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, value, hasValue := strings.Cut(arg, "=")
		if key != "--verify" {
			continue
		}
		if hasValue {
			return strings.ToLower(strings.TrimSpace(value)), true
		}
		if i+1 < len(args) {
			return strings.ToLower(strings.TrimSpace(args[i+1])), true
		}
		return "", true
	}
	return defaultVerifyMode(), false
}

func replaceOrAppendScannerFlag(args []string, flag, value string) []string {
	out := append([]string(nil), args...)
	for i := 1; i < len(out); i++ {
		arg := out[i]
		key, _, hasValue := strings.Cut(arg, "=")
		if key != flag {
			continue
		}
		if hasValue {
			out[i] = flag + "=" + value
			return out
		}
		if i+1 < len(out) {
			out[i+1] = value
			return out
		}
		out = append(out, value)
		return out
	}
	return append(out, flag+"="+value)
}

func removeScannerFlag(args []string, flag string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, _, hasValue := strings.Cut(arg, "=")
		if key != flag {
			out = append(out, arg)
			continue
		}
		if !hasValue && i+1 < len(args) {
			i++
		}
	}
	return out
}
