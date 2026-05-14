package scan

import (
	"fmt"
)

type eventKind string

const (
	eventTarget  eventKind = "target"
	eventFinding eventKind = "finding"
	eventError   eventKind = "error"
)

type event struct {
	Kind    eventKind
	Source  string
	Raw     string
	Target  target
	Finding finding
	Error   errorEvent
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

func (e event) key() string {
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
