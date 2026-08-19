package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/tui"
	ioaclient "github.com/chainreactors/ioa/client"
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

func RunIOAClientCommand(ctx context.Context, mode cfg.RunMode, option *cfg.Option, args cfg.IOAClientArgs, logger telemetry.Logger) error {
	ioaURL := option.IOAURL
	if ioaURL == "" {
		ioaURL = "http://127.0.0.1:8765"
	}
	client, err := ioaclient.NewClient(ioaURL, "")
	if err != nil {
		return fmt.Errorf("connect to server: %w", err)
	}
	if client.AccessKey() != "" {
		if err := client.EnsureRegistered(ctx, "aiscan-cli", "", nil); err != nil {
			return fmt.Errorf("server auth register: %w", err)
		}
	}
	switch mode {
	case cfg.RunModeIOASpaces:
		return tui.RunIOASpaces(ctx, client, option, os.Stdout, os.Stderr)
	case cfg.RunModeIOAMessages:
		return tui.RunIOAMessages(ctx, client, option, args, os.Stdout, os.Stderr)
	case cfg.RunModeIOAContext:
		return tui.RunIOAContext(ctx, client, option, args, os.Stdout, os.Stderr)
	case cfg.RunModeIOANodes:
		return tui.RunIOANodes(ctx, client, option, args, os.Stdout, os.Stderr)
	default:
		return fmt.Errorf("unknown server mode: %s", mode)
	}
}

func ResolveIOANodeName(option *cfg.Option) string {
	if option != nil && option.IOANodeName != "" {
		return option.IOANodeName
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "aiscan-" + hex.EncodeToString(b[:])
	}
	return "aiscan-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
