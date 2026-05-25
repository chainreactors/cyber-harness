//go:build !full

package engine

import "github.com/chainreactors/aiscan/pkg/telemetry"

func (e *Set) SetupUncover(_ ReconOptions, _ telemetry.Logger) {}
