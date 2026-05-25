//go:build full

package cmd

type scannerCommands struct {
	Scan     struct{} `command:"scan" description:"Run the scan pipeline"`
	Cyberhub struct{} `command:"cyberhub" description:"Search Cyberhub fingerprints and POCs"`
	Gogo     struct{} `command:"gogo" description:"Run gogo scanner"`
	Spray    struct{} `command:"spray" description:"Run spray scanner"`
	Katana   struct{} `command:"katana" description:"Run katana web crawler"`
	Zombie   struct{} `command:"zombie" description:"Run zombie weakpass scanner"`
	Neutron  struct{} `command:"neutron" description:"Run neutron POC scanner"`
	Passive  struct{} `command:"passive" description:"Run passive cyberspace recon"`
}

func scannerCommandAvailable(name string) bool {
	switch name {
	case "scan", "cyberhub", "gogo", "spray", "katana", "zombie", "neutron", "passive":
		return true
	default:
		return false
	}
}

func scannerUsageLines() string {
	return `  gogo           Run gogo directly
  spray          Run spray directly
  katana         Run katana web crawler
  zombie         Run zombie directly
  neutron        Run neutron directly
  passive        Run passive cyberspace recon`
}

func cliCommandSummary() string {
	return "agent, ioa serve, scan, cyberhub, gogo, spray, katana, zombie, neutron, or passive"
}
