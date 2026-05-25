//go:build !full

package cmd

type scannerCommands struct {
	Scan     struct{} `command:"scan" description:"Run the scan pipeline"`
	Cyberhub struct{} `command:"cyberhub" description:"Search Cyberhub fingerprints and POCs"`
	Gogo     struct{} `command:"gogo" description:"Run gogo scanner"`
	Spray    struct{} `command:"spray" description:"Run spray scanner"`
	Zombie   struct{} `command:"zombie" description:"Run zombie weakpass scanner"`
	Neutron  struct{} `command:"neutron" description:"Run neutron POC scanner"`
}

func scannerCommandAvailable(name string) bool {
	switch name {
	case "scan", "cyberhub", "gogo", "spray", "zombie", "neutron":
		return true
	default:
		return false
	}
}

func scannerUsageLines() string {
	return `  gogo           Run gogo directly
  spray          Run spray directly
  zombie         Run zombie directly
  neutron        Run neutron directly`
}

func cliCommandSummary() string {
	return "agent, ioa serve, scan, cyberhub, gogo, spray, zombie, or neutron"
}
