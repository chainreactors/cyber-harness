package scan

import (
	"fmt"
	"strings"

	"github.com/chainreactors/parsers"
)

type serviceResult struct {
	Result *parsers.GOGOResult
}

type webProbeResult struct {
	Source     string
	Result     *parsers.SprayResult
	HostHeader string
}

func reportableSprayResult(result *parsers.SprayResult) bool {
	return result != nil && result.IsValid && strings.TrimSpace(result.ErrString) == ""
}

type weakpassResult struct {
	Source string
	Result *parsers.ZombieResult
}

type fingerprintFinding struct {
	Target  string
	Fingers []string
}

func (f fingerprintFinding) Kind() findingKind { return findingFingerprint }

func (f fingerprintFinding) Priority() priority { return priorityLow }

func (f fingerprintFinding) Key() string {
	return strings.ToLower(f.Target) + "|" + strings.Join(parsers.NormalizeNames(f.Fingers), ",")
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
	Message string
}

func (f vulnFinding) Kind() findingKind { return findingVuln }

func (f vulnFinding) Priority() priority { return priorityHigh }

func (f vulnFinding) Key() string { return f.Message }

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
