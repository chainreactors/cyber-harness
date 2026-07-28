//go:build !full

package engine

import "github.com/chainreactors/aiscan/core/telemetry"

func (e *Set) SetupUncover(_ ReconOptions, _ telemetry.Logger) {}
