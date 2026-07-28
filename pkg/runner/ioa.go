package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
)

func RunIOAServe(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	if IOAServeFunc == nil {
		return fmt.Errorf("ioa server not available in this build")
	}
	return IOAServeFunc(ctx, option, logger)
}

func RunIOAClientCommand(ctx context.Context, mode cfg.RunMode, option *cfg.Option, args cfg.IOAClientArgs, logger telemetry.Logger) error {
	if IOAClientCommandFunc == nil {
		return fmt.Errorf("ioa commands not available in this build")
	}
	return IOAClientCommandFunc(ctx, mode, option, args, logger)
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
