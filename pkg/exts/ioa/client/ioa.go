package client

import (
	"context"
	"encoding/json"
	"time"

	inboxpkg "github.com/chainreactors/aiscan/agent/inbox"
	"github.com/chainreactors/aiscan/core/telemetry"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
)

// ---------------------------------------------------------------------------
// IOA inbox subscription
// ---------------------------------------------------------------------------

func subscribeIOASpace(ctx context.Context, stream ioaclient.StreamAPI, spaceID string, nodeID func() string, push func(inboxpkg.Message) error, logger telemetry.Logger, report func(error)) {
	if ctx == nil || isNilIOADependency(stream) || spaceID == "" || push == nil {
		return
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	for attempt := 0; ctx.Err() == nil; attempt++ {
		msgs, errs, cancel, err := stream.Subscribe(ctx, spaceID)
		if err != nil {
			if report != nil {
				report(err)
			}
			delay := retryDelay(attempt)
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
				if nodeID != nil && msg.Sender == nodeID() {
					continue
				}
				m := inboxpkg.NewMessage(inboxpkg.OriginPeer, "user", formatIOAMessage(msg))
				m.Meta = map[string]any{"sender": msg.Sender, "message_id": msg.ID}
				if err := push(m); err != nil {
					logger.Warnf("inbox push ioa: %s", err)
				}
			case err := <-errs:
				if err != nil && report != nil {
					report(err)
				}
				goto reconnect
			case <-ctx.Done():
				cancel()
				for range msgs {
				}
				return
			}
		}
	reconnect:
		cancel()
		for range msgs {
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryDelay(attempt)):
		}
	}
}

func formatIOAMessage(msg protocols.Message) string {
	if text, ok := msg.Content["text"].(string); ok {
		return text
	}
	data, _ := json.Marshal(msg.Content)
	return string(data)
}

func retryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 4 {
		attempt = 4
	}
	return time.Second << uint(attempt)
}
