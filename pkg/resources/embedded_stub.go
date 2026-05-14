//go:build !generated_templates

package resources

// loadEmbeddedConfig returns minimal valid empty JSON for shared resource types
// so that callers like loadLocalFingers (which call LoadFingers directly without
// a fallback) don't get an "unexpected end of JSON input" error and abort
// scanner initialisation.
//
// Engine-specific keys (spray_*, zombie_*) return nil so that each engine falls
// through to its own rich embedded data instead of receiving an empty list.
func loadEmbeddedConfig(typ string) []byte {
	switch typ {
	case "http", "socket", "port", "extract", "workflow", "neutron",
		"fingerprinthub_web", "fingerprinthub_service":
		return []byte("[]")
	default:
		return nil
	}
}
