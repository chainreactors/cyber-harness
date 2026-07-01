package scan

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/utils/parsers"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
)

type eventKind string

const (
	eventTarget eventKind = "target"
	eventLoot   eventKind = "loot"
	eventError  eventKind = "error"
	eventStats  eventKind = "stats"
)

var statsEventSeq uint64

type event struct {
	Kind   eventKind
	Source string
	Raw    string
	Target target
	Loot   *output.Loot
	Error  errorEvent
	Stats  sdktypes.Stats
}

func targetEvent(source, raw string, target target) event {
	if raw == "" && target != nil {
		raw = target.RawInput()
	}
	return event{Kind: eventTarget, Source: source, Raw: raw, Target: target}
}

func lootEvent(source string, loot output.Loot) event {
	return event{Kind: eventLoot, Source: source, Loot: &loot}
}

func errorEventOf(source, message string) event {
	return event{Kind: eventError, Source: source, Error: errorEvent{Message: message}}
}

func statsEvent(source string, stats sdktypes.Stats) event {
	seq := atomic.AddUint64(&statsEventSeq, 1)
	return event{Kind: eventStats, Source: source, Raw: strconv.FormatUint(seq, 10), Stats: stats}
}

func (e event) Key() string {
	switch e.Kind {
	case eventTarget:
		if e.Target == nil {
			return ""
		}
		return fmt.Sprintf("%s|%s", e.Target.Kind(), e.Target.Key())
	case eventLoot:
		if e.Loot == nil {
			return ""
		}
		return e.Loot.Key()
	case eventError:
		return string(eventError) + "|" + e.Error.Message
	case eventStats:
		if e.Raw == "" {
			return ""
		}
		return string(eventStats) + "|" + e.Raw
	default:
		return ""
	}
}

func (e event) label() string {
	switch e.Kind {
	case eventTarget:
		if e.Target != nil {
			return string(e.Target.Kind())
		}
	case eventLoot:
		if e.Loot != nil {
			return e.Loot.Kind
		}
	case eventError:
		return string(eventError)
	case eventStats:
		return string(eventStats)
	}
	return string(e.Kind)
}

type errorEvent struct {
	Message string
}

func emitError(emit func(event), source, format string, args ...any) {
	emit(errorEventOf(source, fmt.Sprintf(format, args...)))
}

type priority string

const (
	priorityLow      priority = "low"
	priorityMedium   priority = "medium"
	priorityHigh     priority = "high"
	priorityCritical priority = "critical"
)

func parsePriority(value string) (priority, error) {
	switch priority(strings.ToLower(strings.TrimSpace(value))) {
	case "", priorityHigh:
		return priorityHigh, nil
	case priorityLow:
		return priorityLow, nil
	case priorityMedium:
		return priorityMedium, nil
	case priorityCritical:
		return priorityCritical, nil
	default:
		return "", fmt.Errorf("unknown priority %q, expected low, medium, high, or critical", value)
	}
}

func (p priority) atLeast(min priority) bool {
	return p.rank() >= min.rank()
}

func (p priority) rank() int {
	switch p {
	case priorityLow:
		return 1
	case priorityMedium:
		return 2
	case priorityHigh:
		return 3
	case priorityCritical:
		return 4
	default:
		return 0
	}
}

func reportableSprayResult(result *parsers.SprayResult) bool {
	if result == nil || !result.IsValid || result.IsFuzzy || strings.TrimSpace(result.ErrString) != "" {
		return false
	}
	switch result.Source {
	case parsers.InitIndexSource, parsers.InitRandomSource:
		return false
	default:
		return true
	}
}

func reportableSprayResultForCapability(result *parsers.SprayResult, capability string) bool {
	if !reportableSprayResult(result) {
		return false
	}
	return capability == capSprayCheck || result.Source != parsers.CheckSource
}

// --- Loot constructors ---

func fingerprintLoot(target string, fingers []string, focus bool) output.Loot {
	pri := string(priorityLow)
	if focus {
		pri = string(priorityHigh)
	}
	return output.Loot{
		Kind:        output.LootFingerprint,
		Target:      target,
		Priority:    pri,
		Description: strings.Join(fingers, ", "),
		Tags:        fingers,
		Data: map[string]any{
			"key":     strings.ToLower(target) + "|" + strings.Join(fingers, ","),
			"fingers": fingers,
			"focus":   focus,
		},
	}
}

func weakpassLoot(result *parsers.ZombieResult) output.Loot {
	desc := result.Service
	if result.Username != "" || result.Password != "" {
		desc += " " + result.Username + "/" + result.Password
	}
	return output.Loot{
		Kind:        output.LootWeakpass,
		Target:      result.Address(),
		Priority:    string(priorityHigh),
		Description: desc,
		Tags:        []string{result.Service},
		Data: map[string]any{
			"key":      fmt.Sprintf("%s|%s|%s|%s", result.Service, result.Address(), result.Username, result.Password),
			"service":  result.Service,
			"username": result.Username,
			"password": result.Password,
		},
	}
}

func vulnLoot(result *sdktypes.VulnResult) output.Loot {
	pri := string(priorityHigh)
	switch result.Severity {
	case "critical":
		pri = string(priorityCritical)
	case "high":
		pri = string(priorityHigh)
	case "medium":
		pri = string(priorityMedium)
	case "info":
		pri = string(priorityLow)
	}
	desc := result.TemplateID
	if result.TemplateName != "" {
		desc += " — " + result.TemplateName
	}
	return output.Loot{
		Kind:        output.LootVuln,
		Target:      result.Target,
		Priority:    pri,
		Description: desc,
		Tags:        []string{result.Severity, result.TemplateID},
		Data: map[string]any{
			"key":           result.Target + "|" + result.TemplateID,
			"template_id":   result.TemplateID,
			"template_name": result.TemplateName,
			"severity":      result.Severity,
		},
	}
}
