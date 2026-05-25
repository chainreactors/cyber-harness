//go:build e2e && full

package e2e

func buildTags() string { return "emptytemplates noembed full" }

func scannerHelpCommands() []string {
	return []string{"gogo", "spray", "katana", "zombie", "neutron", "passive", "scan"}
}
