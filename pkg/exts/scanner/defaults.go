package scanner

// Link-time defaults for the scanner distribution's configuration sections.
// Editions and forks override these at build time; section factories read
// them on every call, so "set the variable, then resolve" keeps working.
var (
	DefaultCyberhubURL  = ""
	DefaultCyberhubKey  = ""
	DefaultCyberhubMode = "merge"
	DefaultScannerProxy = ""
)
