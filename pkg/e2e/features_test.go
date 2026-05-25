//go:build e2e && !full

package harness

func buildTags() string { return "emptytemplates noembed" }

func scannerHelpCommands() []string {
	return []string{"gogo", "spray", "zombie", "neutron", "scan"}
}
