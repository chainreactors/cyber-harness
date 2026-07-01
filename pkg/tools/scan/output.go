package scan

import (
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/utils/parsers"
)

func verificationLabel(status string) string {
	switch status {
	case "confirmed":
		return "[verified]"
	case "not_confirmed":
		return "[not confirmed]"
	case "inconclusive":
		return "[inconclusive]"
	default:
		return "[ai:" + status + "]"
	}
}

func formatEventLine(event event, color bool) string {
	c := output.NewColor(color)
	switch event.Kind {
	case eventTarget:
		switch target := event.Target.(type) {
		case serviceTarget:
			if target.Result == nil {
				return ""
			}
			label := "service"
			if target.Result.IsHttp() {
				label = "web"
			}
			return output.FormatLine(output.OutputPrefix(label, c.Green), target.Result.OutputLine(), c)
		case webTarget:
			if target.URL == "" {
				return ""
			}
			return output.FormatLine(output.OutputPrefix("web", c.Green), parsers.JoinOutput(target.URL, target.HostHeader), c)
		case webProbeTarget:
			if !reportableSprayResultForCapability(target.Result, target.Capability) {
				return ""
			}
			return output.FormatLine(output.OutputPrefix("web", c.Green), target.Result.OutputLine(), c)
		}
	case eventLoot:
		if event.Loot == nil {
			return ""
		}
		loot := event.Loot
		var label string
		switch loot.Kind {
		case output.LootFingerprint:
			focus, _ := loot.Data["focus"].(bool)
			if !focus {
				return ""
			}
			label = "fingerprint"
		case output.LootWeakpass:
			label = "risk"
		case output.LootVuln:
			label = "vuln"
		default:
			label = loot.Kind
		}
		if status, _ := loot.Data["verification_status"].(string); status != "" {
			label = verificationLabel(status) + " " + label
		}
		return output.FormatLine(output.OutputPrefix(label, c.ForPriority(loot.Priority)), loot.Description, c)
	case eventError:
		if event.Error.Message == "" {
			return ""
		}
		return output.FormatLine(output.OutputPrefix("error", c.Red), parsers.JoinOutput(event.Error.Message), c)
	}
	return ""
}
