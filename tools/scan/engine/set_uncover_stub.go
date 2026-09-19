//go:build !full

package engine

import "github.com/chainreactors/cyber/core/telemetry"

func (e *Set) SetupUncover(_ ReconOptions, _ telemetry.Logger) {}
