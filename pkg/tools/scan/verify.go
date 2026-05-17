package scan

import (
	"strings"

	"github.com/chainreactors/parsers"
)

func findingKindOf(finding finding) findingKind {
	if finding == nil {
		return ""
	}
	return finding.Kind()
}

func findingPriority(finding finding) priority {
	if finding == nil {
		return priorityLow
	}
	return finding.Priority()
}

func findingTarget(finding finding) string {
	switch f := finding.(type) {
	case fingerprintFinding:
		return f.Target
	case weakpassFinding:
		if f.Result != nil {
			return f.Result.URI()
		}
	case vulnFinding:
		return f.Target
	case verificationFinding:
		return f.Target
	case aiSkillFinding:
		return f.Target
	}
	return ""
}

func findingEvidence(finding finding) string {
	switch f := finding.(type) {
	case fingerprintFinding:
		prefix := "fingerprint "
		if f.Focus {
			prefix = "focus fingerprint "
		}
		return strings.TrimSpace(prefix + strings.Join(parsers.NormalizeNames(f.Fingers), ","))
	case weakpassFinding:
		if f.Result != nil {
			return f.Result.OutputLine()
		}
	case vulnFinding:
		return f.String()
	case verificationFinding:
		return f.Summary
	case aiSkillFinding:
		if f.Summary != "" {
			return f.Summary
		}
		return f.Detail
	}
	return ""
}

