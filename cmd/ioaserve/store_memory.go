//go:build !sqlite

package ioaserve

import (
	"github.com/chainreactors/aiscan/pkg/telemetry"
	ioaserver "github.com/chainreactors/ioa/server"
)

func openStore(dbPath string, logger telemetry.Logger) (ioaserver.Store, func() error, error) {
	logger.Warnf("ioa_server store=memory: --ioa-db=%q ignored, all state will be lost on restart (rebuild with -tags sqlite to enable persistence)", dbPath)
	return nil, nil, nil
}
