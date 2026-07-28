package capability

// The queries here replace the Extra* globals that core/config used to carry:
// CLI availability, the usage block, the command summary and the lazy usage
// text all now come from the descriptors that are actually linked.

// CLIAvailable reports whether name is a top-level CLI command in this binary.
func CLIAvailable(name string) bool {
	_, ok := byCLIName(name)
	return ok
}

// UsageLines returns the pre-aligned usage rows of every linked CLI
// capability, in registration order.
func UsageLines() []string {
	var out []string
	for _, d := range All() {
		if d.CLIName == "" || d.UsageLine == "" {
			continue
		}
		out = append(out, d.UsageLine)
	}
	return out
}

// Summaries returns the summary words of every linked CLI capability, in
// registration order. Callers prepend the binary's own modes (agent, web, …).
func Summaries() []string {
	var out []string
	for _, d := range All() {
		if d.CLIName == "" || d.Summary == "" {
			continue
		}
		out = append(out, d.Summary)
	}
	return out
}

// Usage renders a CLI capability's help text, or false when the capability is
// not linked or declares no usage.
func Usage(name string) (string, bool) {
	d, ok := byCLIName(name)
	if !ok || d.Usage == nil {
		return "", false
	}
	return d.Usage(), true
}

func byCLIName(name string) (Descriptor, bool) {
	if name == "" {
		return Descriptor{}, false
	}
	for _, d := range All() {
		if d.CLIName == name {
			return d, true
		}
	}
	return Descriptor{}, false
}
