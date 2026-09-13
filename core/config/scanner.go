package config

type ScannerCommands struct {
	Scan    struct{} `command:"scan" description:"Run the scan pipeline"`
	Gogo    struct{} `command:"gogo" description:"Run gogo scanner"`
	Spray   struct{} `command:"spray" description:"Run spray scanner"`
	Katana  struct{} `command:"katana" description:"Run katana web crawler"`
	Zombie  struct{} `command:"zombie" description:"Run zombie weakpass scanner"`
	Neutron struct{} `command:"neutron" description:"Run neutron POC scanner"`
	Proton  struct{} `command:"proton" description:"Run proton sensitive info scanner"`
	Passive struct{} `command:"passive" description:"Run passive cyberspace recon"`
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
