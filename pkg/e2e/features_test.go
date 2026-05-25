//go:build e2e && !full

package e2e

func buildTags() string { return "emptytemplates noembed" }

func scannerHelpCommands() []string {
	return []string{"gogo", "spray", "zombie", "neutron", "scan"}
}
