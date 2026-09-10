package runner

import (
	"context"
	"net/url"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

func RunIOAServe(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	store := ioaserver.NewMemoryStore()
	logger.Importantf("aiscan server store=memory")
	defer func() { _ = store.Close() }()

	accessKey := option.IOAToken
	if accessKey == "" {
		accessKey = protocols.NewToken()
	}
	listenURL := option.IOAURL
	if listenURL == "" {
		listenURL = "http://127.0.0.1:8765"
	}
	if parsed, err := url.Parse(listenURL); err == nil {
		logger.Infof("  agent IOA connect: aiscan agent --transport local --ioa-url http://%s@%s", accessKey, parsed.Host)
	}
	return ioaserver.RunServer(ctx, ioaserver.ServerOptions{URL: listenURL, AccessKey: accessKey, Store: store})
}
