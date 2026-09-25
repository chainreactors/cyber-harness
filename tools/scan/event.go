package scan

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	sdktypes "github.com/chainreactors/sdk/pkg/types"
	"github.com/chainreactors/utils/parsers"
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
	Kind     eventKind
	Source   string
	Target   target
	Loot     *parsers.Loot
	Artifact *artifactResult
	Error    string
	Stats    sdktypes.Stats
	statsSeq uint64
}

// artifactResult carries scanner-native data to the accepted-event boundary.
// It stays internal because the canonical output is built by toolargs.Base.
type artifactResult struct {
	ResultID string
	Tool     string
	Kind     string
	Target   string
	Data     any
}

func targetEvent(source string, target target) event {
	return event{Kind: eventTarget, Source: source, Target: target}
}

func lootEvent(source string, loot parsers.Loot) event {
	return event{Kind: eventLoot, Source: source, Loot: &loot}
}

func bindLoot(loot parsers.Loot, resultID, tool string) parsers.Loot {
	if loot.Data == nil {
		loot.Data = make(map[string]any)
	}
	loot.Data["result_id"] = resultID
	loot.Data["artifact_tool"] = tool
	return loot
}

func artifactLootEvent(source string, loot parsers.Loot, artifact artifactResult) event {
	return event{Kind: eventLoot, Source: source, Loot: &loot, Artifact: &artifact}
}

func errorEventOf(source, message string) event {
	return event{Kind: eventError, Source: source, Error: message}
}

func statsEvent(source string, stats sdktypes.Stats) event {
	return event{Kind: eventStats, Source: source, Stats: stats, statsSeq: atomic.AddUint64(&statsEventSeq, 1)}
}

func (e event) Key() string {
	switch e.Kind {
	case eventTarget:
		if e.Target == nil {
			return ""
		}
		if e.Target.Kind() == targetWebProbe {
			return fmt.Sprintf("%s|%s|%s", e.Target.Kind(), e.Source, e.Target.Key())
		}
		return fmt.Sprintf("%s|%s", e.Target.Kind(), e.Target.Key())
	case eventLoot:
		if e.Loot == nil {
			return ""
		}
		return e.Loot.Key()
	case eventError:
		return string(eventError) + "|" + e.Error
	case eventStats:
		if e.statsSeq == 0 {
			return ""
		}
		return string(eventStats) + "|" + strconv.FormatUint(e.statsSeq, 10)
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

func emitError(emit func(event), source, format string, args ...any) {
	emit(errorEventOf(source, fmt.Sprintf(format, args...)))
}

const (
	priorityLow      = "low"
	priorityMedium   = "medium"
	priorityHigh     = "high"
	priorityCritical = "critical"
)

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

func fingerprintLoot(target string, fingers []string, focus bool) parsers.Loot {
	pri := priorityLow
	if focus {
		pri = priorityHigh
	}
	return parsers.Loot{
		Kind:        parsers.LootFingerprint,
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

func weakpassLoot(result *parsers.ZombieResult) parsers.Loot {
	desc := result.Service
	if result.Username != "" || result.Password != "" {
		desc += " " + result.Username + "/" + result.Password
	}
	return parsers.Loot{
		Kind:        parsers.LootWeakpass,
		Target:      result.Address(),
		Priority:    priorityHigh,
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

func vulnLoot(result *sdktypes.TemplateResult) parsers.Loot {
	pri := priorityHigh
	switch result.Severity {
	case "critical":
		pri = priorityCritical
	case "medium":
		pri = priorityMedium
	case "info":
		pri = priorityLow
	}
	desc := result.TemplateID
	if result.TemplateName != "" {
		desc += " — " + result.TemplateName
	}
	return parsers.Loot{
		Kind:        parsers.LootVuln,
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
