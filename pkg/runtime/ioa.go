package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/chainreactors/aiscan/agent"
	inboxpkg "github.com/chainreactors/aiscan/agent/inbox"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
)

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

// ---------------------------------------------------------------------------
// IOA inbox subscription
// ---------------------------------------------------------------------------

func subscribeIOASpace(ctx context.Context, stream ioaclient.StreamAPI, spaceID, nodeID string, push func(inboxpkg.Message) error, logger telemetry.Logger) {
	if ctx == nil || isNilIOADependency(stream) || spaceID == "" || push == nil {
		return
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	for attempt := 0; ctx.Err() == nil; attempt++ {
		msgs, errs, cancel, err := stream.Subscribe(ctx, spaceID)
		if err != nil {
			delay := agent.RetryDelay(attempt)
			logger.Debugf("ioa subscribe: %s, retry in %s", err, delay)
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return
			}
		}
		attempt = 0
		logger.Debugf("ioa subscribed to space %s", spaceID)
		for {
			select {
			case msg, ok := <-msgs:
				if !ok {
					goto reconnect
				}
				if msg.Sender == nodeID {
					continue
				}
				m := inboxpkg.NewMessage(inboxpkg.OriginPeer, "user", formatIOAMessage(msg))
				m.Meta = map[string]any{"sender": msg.Sender, "message_id": msg.ID}
				if err := push(m); err != nil {
					logger.Warnf("inbox push ioa: %s", err)
				}
			case <-errs:
				goto reconnect
			case <-ctx.Done():
				cancel()
				return
			}
		}
	reconnect:
		cancel()
	}
}

func formatIOAMessage(msg protocols.Message) string {
	if text, ok := msg.Content["text"].(string); ok {
		return text
	}
	data, _ := json.Marshal(msg.Content)
	return string(data)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type memoryIdentity struct{ ref protocols.NodeRef }

func (i memoryIdentity) IOABinding() protocols.IdentityBinding {
	return protocols.IdentityBinding{
		Namespace: "aiscan.memory",
		Subject:   i.ref.URI(),
	}
}

func registerIOATools(ctx context.Context, application *apppkg.App, option *cfg.Option) error {
	ioaURL := option.IOAURL
	if ioaURL == "" {
		return nil
	}
	ioaCfg := apppkg.IOAConfig{
		URL:           ioaURL,
		NodeID:        option.IOANodeID,
		NodeName:      option.IOANodeName,
		Space:         option.Space,
		RegisterTools: true,
		AutoRegister:  true,
		NodeMeta:      map[string]any{"client": "aiscan"},
		Identity: memoryIdentity{ref: protocols.NodeRef{
			ID: protocols.NewID(), Authority: "memory://aiscan",
		}},
	}
	if ioaCfg.NodeName == "" {
		ioaCfg.NodeName = ResolveIOANodeName(option)
	}
	return application.InitIOA(ctx, ioaCfg)
}
