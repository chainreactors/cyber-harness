//go:build full

package engine

import (
	"strings"

	"github.com/chainreactors/cyber/core/telemetry"
)

func (e *Set) SetupUncover(opts ReconOptions, logger telemetry.Logger) {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	eng := NewUncoverEngine(opts, logger)
	if len(eng.Sources()) == 0 {
		return
	}
	e.Uncover = eng
	logger.Infof("%s", telemetry.StartupOK("uncover", strings.Join(e.Uncover.Sources(), ",")))
}
