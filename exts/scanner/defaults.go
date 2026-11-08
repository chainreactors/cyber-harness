package scanner

// Link-time defaults for the scanner distribution's configuration sections.
// Editions and forks override these at build time. Startup overrides use
// SetDefaults; section factories read the current defaults on every call.
var (
	DefaultCyberhubURL  = ""
	DefaultCyberhubKey  = ""
	DefaultCyberhubMode = "merge"
	DefaultScannerProxy = ""
)

// SetDefaults replaces the link-time scanner defaults and returns a restore
// function. Call only during startup, before configuration is resolved.
// Mitm remains a per-host option, not a link-time default.
func SetDefaults(value CyberhubOptions) func() {
	url, key, mode, proxy := DefaultCyberhubURL, DefaultCyberhubKey, DefaultCyberhubMode, DefaultScannerProxy
	DefaultCyberhubURL, DefaultCyberhubKey, DefaultCyberhubMode, DefaultScannerProxy = value.URL, value.Key, value.Mode, value.Proxy
	return func() {
		DefaultCyberhubURL, DefaultCyberhubKey, DefaultCyberhubMode, DefaultScannerProxy = url, key, mode, proxy
	}
}
