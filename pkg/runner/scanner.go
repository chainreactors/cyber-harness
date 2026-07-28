package runner

import (
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/core/config"
)

func DirectScannerRuntimeFeatures(rest []string) (RuntimeFeatures, []string, error) {
	if len(rest) == 0 {
		return RuntimeFeatures{}, nil, fmt.Errorf("missing scanner command")
	}
	if rest[0] != "scan" {
		return RuntimeFeatures{}, rest, nil
	}
	verifyMode, explicit := scannerVerifyMode(rest[1:])
	sniperEnabled := HasScannerFlag(rest[1:], "--sniper")
	deepEnabled := HasScannerFlag(rest[1:], "--deep")
	aiSkillRequested := sniperEnabled || deepEnabled

	features := RuntimeFeatures{}

	if aiSkillRequested {
		features.ProviderEnabled = true
		features.ProviderOptional = false
		features.AIEnabled = true
		features.ScannerAI = true
	}

	switch verifyMode {
	case "auto":
		features.ProviderEnabled = true
		if !aiSkillRequested {
			features.ProviderOptional = true
		}
		features.AIEnabled = true
		features.ScannerAI = explicit || aiSkillRequested
		return features, removeScannerFlag(rest, "--verify"), nil
	case "off":
		if explicit {
			return features, replaceOrAppendScannerFlag(rest, "--verify", "off"), nil
		}
		return features, rest, nil
	case "low", "medium", "high", "critical":
		features.ProviderEnabled = true
		if !aiSkillRequested {
			features.ProviderOptional = !explicit
		}
		features.AIEnabled = true
		features.ScannerAI = explicit || aiSkillRequested
		return features, rest, nil
	default:
		if explicit {
			return RuntimeFeatures{}, nil, fmt.Errorf("invalid --verify value %q: expected auto, off, low, medium, high, or critical", verifyMode)
		}
		return features, rest, nil
	}
}

func HasScannerFlag(args []string, long string) bool {
	for _, arg := range args {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			return true
		}
	}
	return false
}

func ShouldStreamScannerOutput(rest []string) bool {
	if len(rest) == 0 || rest[0] != "scan" {
		return false
	}
	if isDirectScannerJSONOutput(rest) {
		return false
	}
	for _, arg := range rest[1:] {
		if arg == "--report" {
			return false
		}
		if strings.HasPrefix(arg, "--report=") {
			value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--report=")))
			if value != "false" && value != "0" && value != "no" {
				return false
			}
		}
	}
	return true
}

func isDirectScannerJSONOutput(rest []string) bool {
	if len(rest) == 0 || !config.ScannerCommandAvailable(rest[0]) {
		return false
	}
	for _, arg := range rest[1:] {
		if arg == "-j" || arg == "--json" {
			return true
		}
		if strings.HasPrefix(arg, "--json=") {
			value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--json=")))
			return value != "false" && value != "0" && value != "no"
		}
	}
	return false
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

func defaultVerifyMode() string {
	value := strings.ToLower(strings.TrimSpace(config.DefaultVerify))
	if value == "" {
		return "off"
	}
	return value
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
