package scan

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/chainreactors/parsers"
	sdkkit "github.com/chainreactors/sdk/pkg"
)

type eventKind string

const (
	eventTarget  eventKind = "target"
	eventFinding eventKind = "finding"
	eventError   eventKind = "error"
	eventStats   eventKind = "stats"
)

var statsEventSeq uint64

type event struct {
	Kind    eventKind
	Source  string
	Raw     string
	Target  target
	Finding finding
	Error   errorEvent
	Stats   sdkkit.Stats
}

func targetEvent(source, raw string, target target) event {
	if raw == "" && target != nil {
		raw = target.RawInput()
	}
	return event{Kind: eventTarget, Source: source, Raw: raw, Target: target}
}

func findingEvent(source string, finding finding) event {
	return event{Kind: eventFinding, Source: source, Finding: finding}
}

func errorEventOf(source, message string) event {
	return event{Kind: eventError, Source: source, Error: errorEvent{Message: message}}
}

func statsEvent(source string, stats sdkkit.Stats) event {
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
	case eventFinding:
		if e.Finding == nil {
			return ""
		}
		return fmt.Sprintf("%s|%s", e.Finding.Kind(), e.Finding.Key())
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
	case eventFinding:
		if e.Finding != nil {
			return string(e.Finding.Kind())
		}
	case eventError:
		return string(eventError)
	case eventStats:
		return string(eventStats)
	}
	return string(e.Kind)
}

type finding interface {
	Kind() findingKind
	Key() string
	Priority() priority
}

type findingKind string

const (
	findingFingerprint  findingKind = "fingerprint"
	findingWeakpass     findingKind = "weakpass-finding"
	findingVuln         findingKind = "vuln-finding"
	findingVerification findingKind = "verification-finding"
)

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

func reportableVerificationFinding(finding verificationFinding) bool {
	return finding.Status == verificationConfirmed && (finding.Target != "" || finding.Summary != "" || finding.Evidence != "")
}

type fingerprintFinding struct {
	Target  string
	Fingers []string
	Focus   bool
}

func (f fingerprintFinding) Kind() findingKind { return findingFingerprint }

func (f fingerprintFinding) Priority() priority {
	if f.Focus {
		return priorityHigh
	}
	return priorityLow
}

func (f fingerprintFinding) Key() string {
	return strings.ToLower(f.Target) + "|" + strings.Join(parsers.NormalizeNames(f.Fingers), ",") + fmt.Sprintf("|focus=%t", f.Focus)
}

type weakpassFinding struct {
	Result *parsers.ZombieResult
}

func (f weakpassFinding) Kind() findingKind { return findingWeakpass }

func (f weakpassFinding) Priority() priority { return priorityHigh }

func (f weakpassFinding) Key() string {
	if f.Result == nil {
		return ""
	}
	return fmt.Sprintf("%s|%s|%s|%s|%d",
		strings.ToLower(f.Result.Service),
		strings.ToLower(f.Result.Address()),
		f.Result.Username,
		f.Result.Password,
		f.Result.Mod,
	)
}

type vulnFinding struct {
	Target string
	Output string
}

func (f vulnFinding) Kind() findingKind { return findingVuln }

func (f vulnFinding) Priority() priority { return priorityHigh }

func (f vulnFinding) Key() string { return f.String() }

func (f vulnFinding) String() string {
	return strings.TrimSpace(f.Output)
}

type verificationStatus string

const (
	verificationConfirmed    verificationStatus = "confirmed"
	verificationNotConfirmed verificationStatus = "not_confirmed"
	verificationInconclusive verificationStatus = "inconclusive"
	verificationFailed       verificationStatus = "failed"
)

type verificationFinding struct {
	OriginalKey      string
	OriginalKind     findingKind
	OriginalPriority priority
	Status           verificationStatus
	Target           string
	Summary          string
	Evidence         string
}

func (f verificationFinding) Kind() findingKind { return findingVerification }

func (f verificationFinding) Priority() priority { return f.OriginalPriority }

func (f verificationFinding) Key() string {
	return fmt.Sprintf("%s|%s|%s", f.OriginalKind, f.OriginalKey, f.Status)
}
