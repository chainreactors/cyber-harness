package client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/ioa/protocols"
)

// switchSpace opens the new feed before releasing the old one. The SDK Subscribe
// return is the readiness boundary; no HTTP listener exists for a local client.
func (e *CollaborationExtension) switchSpace(ctx context.Context, space string) error {
	e.receiveMu.Lock()
	defer e.receiveMu.Unlock()
	if err := e.ctx.Err(); err != nil {
		return err
	}
	if space == e.receiveSpace {
		return nil
	}
	feedCtx, cancel := context.WithCancel(e.ctx)
	stopInit := context.AfterFunc(ctx, cancel)
	feed, errs, stop, err := e.service.Client().Subscribe(feedCtx, space)
	stopInit()
	if err != nil {
		cancel()
		return err
	}
	if err := ctx.Err(); err != nil {
		cancel()
		stop()
		return err
	}
	if e.receiveCancel != nil {
		e.receiveCancel()
	}
	e.receiveSpace, e.receiveCancel = space, cancel
	e.workers.Add(1)
	go func() {
		defer e.workers.Done()
		defer cancel()
		for attempt := 0; ; attempt++ {
			for feed != nil {
				select {
				case <-feedCtx.Done():
					stop()
					return
				case msg, ok := <-feed:
					if !ok {
						feed = nil
						break
					}
					if err := e.deliver(feedCtx, msg); err != nil && feedCtx.Err() == nil {
						e.Service().ReportError(err)
					}
				case err := <-errs:
					if err != nil {
						e.Service().ReportError(err)
					}
					feed = nil
				}
			}
			stop()
			select {
			case <-feedCtx.Done():
				return
			case <-time.After(retryDelay(attempt)):
			}
			var err error
			feed, errs, stop, err = e.service.Client().Subscribe(feedCtx, space)
			if err != nil {
				e.Service().ReportError(err)
				stop = func() {}
				continue
			}
			attempt = 0
		}
	}()
	return nil
}

func (e *CollaborationExtension) deliver(ctx context.Context, msg protocols.Message) error {
	if _, recorded := msg.Meta["subagent"]; recorded && msg.ContentType == "handoff" {
		return nil
	}
	node := e.service.Client().NodeID()
	if len(msg.Refs.Nodes) > 0 {
		targeted := false
		for _, id := range msg.Refs.Nodes {
			if id == node {
				targeted = true
			}
		}
		if !targeted {
			return nil
		}
	}
	target, _ := msg.Meta["target_session_id"].(string)
	if msg.Sender == node && target == "" {
		return nil
	}
	e.mu.Lock()
	if e.seen[msg.ID] {
		e.mu.Unlock()
		return nil
	}
	var route *sessionRoute
	if target != "" {
		route = e.routes[target]
		if route == nil {
			for _, candidate := range e.routes {
				if candidate.start.AgentName == target && candidate.deliver != nil {
					if route != nil {
						e.mu.Unlock()
						return fmt.Errorf("IOA session name %q is ambiguous; use its Session ID", target)
					}
					route = candidate
				}
			}
		}
	} else {
		for _, candidate := range e.routes {
			if candidate.primary {
				route = candidate
				break
			}
		}
		if route == nil {
			count := 0
			for _, candidate := range e.routes {
				if !candidate.delegated {
					route = candidate
					count++
				}
			}
			if count != 1 {
				route = nil
			}
		}
	}
	if route == nil || route.deliver == nil {
		e.mu.Unlock()
		return fmt.Errorf("no active IOA receiver for session %q", target)
	}
	deliver := route.deliver
	e.seen[msg.ID] = true
	e.recent = append(e.recent, msg.ID)
	if len(e.recent) > 1024 {
		delete(e.seen, e.recent[0])
		e.recent = e.recent[1:]
	}
	e.mu.Unlock()
	input := inbox.NewMessage(inbox.OriginPeer, "user", formatIOAMessage(msg))
	input.Interrupt, _ = msg.Meta["interrupt"].(bool)
	input.Meta = map[string]any{"sender": msg.Sender, "message_id": msg.ID, "source_session_id": msg.Meta["source_session_id"], "target_session_id": target}
	return deliver(ctx, input)
}

func formatIOAMessage(msg protocols.Message) string {
	text, ok := msg.Content["text"].(string)
	if !ok {
		data, _ := json.Marshal(msg.Content)
		text = string(data)
	}
	source, _ := msg.Meta["source_session_id"].(string)
	target, _ := msg.Meta["target_session_id"].(string)
	if source != "" || target != "" {
		text = fmt.Sprintf("[IOA source_session_id=%s target_session_id=%s]\n%s", source, target, text)
	}
	return text
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
