package config

import (
	"strings"
)

var ExtraCommands = map[string]bool{}

var ExtraUsageEntries []string

var ExtraSummaryEntries []string

var ExtraScannerUsage = map[string]func() string{}

// ScannerEnabled reports whether built-in scanner commands are available.
// Defaults to true; cmd/agent sets it to false.
var ScannerEnabled = true

type ScannerCommands struct {
	Scan    struct{} `command:"scan" description:"Run the scan pipeline"`
	Gogo    struct{} `command:"gogo" description:"Run gogo scanner"`
	Spray   struct{} `command:"spray" description:"Run spray scanner"`
	Katana  struct{} `command:"katana" description:"Run katana web crawler"`
	Zombie  struct{} `command:"zombie" description:"Run zombie weakpass scanner"`
	Neutron struct{} `command:"neutron" description:"Run neutron POC scanner"`
	Passive struct{} `command:"passive" description:"Run passive cyberspace recon"`
}

func ScannerCommandAvailable(name string) bool {
	if !ScannerEnabled {
		return ExtraCommands[name]
	}
	switch name {
	case "scan", "gogo", "spray", "zombie", "neutron":
		return true
	default:
		return ExtraCommands[name]
	}
}

func ScannerUsageLines() string {
	if !ScannerEnabled {
		if len(ExtraUsageEntries) == 0 {
			return ""
		}
		return strings.Join(ExtraUsageEntries, "\n")
	}
	base := `  gogo           Run gogo directly
  spray          Run spray directly
  zombie         Run zombie directly
  neutron        Run neutron directly`
	if len(ExtraUsageEntries) == 0 {
		return base
	}
	return base + "\n" + strings.Join(ExtraUsageEntries, "\n")
}

func CLICommandSummary() string {
	if !ScannerEnabled {
		base := "agent, serve"
		if len(ExtraSummaryEntries) == 0 {
			return base
		}
		return base + ", " + strings.Join(ExtraSummaryEntries, ", ")
	}
	base := "agent, web, serve, scan, gogo, spray, zombie, neutron"
	if len(ExtraSummaryEntries) == 0 {
		return base
	}
	return base + ", " + strings.Join(ExtraSummaryEntries, ", ")
}

func IsScannerHelpRequest(args []string) bool {
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

func StaticScannerUsage(name string) (string, bool) {
	if !ScannerCommandAvailable(name) {
		return "", false
	}
	fn, ok := ExtraScannerUsage[name]
	if !ok || fn == nil {
		return "", false
	}
	return fn(), true
}
