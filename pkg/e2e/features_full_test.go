//go:build e2e && full

package harness

func buildTags() string { return "emptytemplates noembed full" }

func scannerHelpCommands() []string {
	return []string{"gogo", "spray", "katana", "zombie", "neutron", "passive", "scan"}
}
