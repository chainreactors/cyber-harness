//go:build record && cgo && (windows || linux)

package record

import (
	"github.com/chainreactors/cyber/core/resource"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// Declare contributes the record configuration section. Its build tags match
// the extension's exactly: a section the host can validate, emit into --init
// and show in settings, while no extension could ever consume it, is a second
// source of truth about what this build contains.
func Declare(resources *resource.Registry) error {
	_, err := resource.Add[cfg.Section](resources, Section())
	return err
}
