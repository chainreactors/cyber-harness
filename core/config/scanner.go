package config

import (
	"strings"
)

var ExtraCommands = map[string]bool{}

var ExtraUsageEntries []string

var ExtraSummaryEntries []string

var ExtraScannerUsage = map[string]func() string{}

// ScanUsageFunc is set by the scan package init in non-mini builds.
var ScanUsageFunc func() string

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
	switch name {
	case "scan":
		if ScanUsageFunc != nil {
			return ScanUsageFunc(), true
		}
		if !ScannerEnabled {
			return "", false
		}
		return "scan - AI-assisted security scan pipeline\nUsage: scan [options]\n", true
	case "gogo":
		if !ScannerEnabled {
			return "", false
		}
		return "gogo - host, port, service, and banner discovery\nUsage: gogo [options]\n", true
	case "spray":
		if !ScannerEnabled {
			return "", false
		}
		return "spray - web probing, fingerprints, common files, and crawl checks\nUsage: spray [options]\n", true
	case "zombie":
		if !ScannerEnabled {
			return "", false
		}
		return "zombie - weak credential checks for supported services\nUsage: zombie [options]\n", true
	case "neutron":
		if !ScannerEnabled {
			return "", false
		}
		return "neutron - POC/vulnerability testing with nuclei-style options\nUsage: neutron -u <target> [options]\n", true
	default:
		if fn, ok := ExtraScannerUsage[name]; ok {
			return fn(), true
		}
		return "", false
	}
}
