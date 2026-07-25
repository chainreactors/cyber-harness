package scan

import (
	"fmt"
	"slices"
	"strings"
)

func FormatAgentTaskPrompt(scannerArgs []string, intent string) string {
	command := strings.Join(scannerArgs, " ")
	if strings.TrimSpace(intent) == "" {
		return fmt.Sprintf("Execute: %s", command)
	}
	return fmt.Sprintf("Execute: %s\n\nUser intent: %s", command, strings.TrimSpace(intent))
}

func FilterAutoSkill(selected []string, command string) []string {
	auto := ScannerSkillName(command)
	if auto == "" {
		return selected
	}
	out := make([]string, 0, len(selected))
	for _, name := range selected {
		if strings.TrimSpace(name) == auto {
			continue
		}
		out = append(out, name)
	}
	return slices.Clip(out)
}

func ScannerSkillName(command string) string {
	switch command {
	case "gogo", "spray", "katana", "zombie", "neutron", "proton", "passive", "scan":
		return command
	default:
		return ""
	}
}
