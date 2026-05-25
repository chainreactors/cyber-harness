package ioaserve

import (
	"context"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	ioaserver "github.com/chainreactors/ioa/server"
)

type Config struct {
	URL string
	DB  string
}

func RunServe(ctx context.Context, cfg Config, logger telemetry.Logger) error {
	store, closeStore, err := openStore(cfg.DB, logger)
	if err != nil {
		return err
	}
	if closeStore != nil {
		defer func() { _ = closeStore() }()
	}
	return ioaserver.RunServer(ctx, ioaserver.ServerOptions{
		URL:    cfg.URL,
		Store:  store,
		Logger: logger,
	})
}
