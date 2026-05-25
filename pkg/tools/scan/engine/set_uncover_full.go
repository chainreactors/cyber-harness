//go:build full

package engine

import "github.com/chainreactors/aiscan/pkg/telemetry"

func (e *Set) SetupUncover(opts ReconOptions, logger telemetry.Logger) {
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	e.Recon = mergeReconOptions(e.Recon, opts)
	eng := NewUncoverEngine(e.Recon, logger)
	if len(eng.Sources()) == 0 {
		return
	}
	if e.Uncover != nil {
		_ = e.Uncover.Close()
	}
	e.Uncover = eng
	logger.Infof("engine=uncover status=ready sources=%v", e.Uncover.Sources())
}

func mergeReconOptions(base, next ReconOptions) ReconOptions {
	if next.FofaEmail != "" {
		base.FofaEmail = next.FofaEmail
	}
	if next.FofaKey != "" {
		base.FofaKey = next.FofaKey
	}
	if next.HunterToken != "" {
		base.HunterToken = next.HunterToken
	}
	if next.HunterAPIKey != "" {
		base.HunterAPIKey = next.HunterAPIKey
	}
	if next.IngressProxy != "" {
		base.IngressProxy = next.IngressProxy
	}
	if next.Limit != 0 {
		base.Limit = next.Limit
	}
	return base
}
