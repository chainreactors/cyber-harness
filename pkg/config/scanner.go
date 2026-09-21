package config

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
