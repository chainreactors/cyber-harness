package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
		backfill := time.NewTicker(time.Second)
		defer backfill.Stop()
		for attempt := 0; ; attempt++ {
			if err := e.replay(feedCtx, space); err != nil {
				e.Service().ReportError(err)
			}
			for feed != nil {
				select {
				case <-backfill.C:
					if err := e.replay(feedCtx, space); err != nil {
						e.Service().ReportError(err)
					}
				case <-feedCtx.Done():
					stop()
					return
				case msg, ok := <-feed:
					if !ok {
						feed = nil
						break
					}
					for admission := 0; ; admission++ {
						err := e.deliver(feedCtx, msg)
						if err == nil || feedCtx.Err() != nil {
							break
						}
						e.Service().ReportError(err)
						if e.options.Receive == nil || !errors.Is(err, inbox.ErrInboxFull) {
							break
						}
						// Retain this event until the router admits it. Sleeping
						// outside e.mu keeps session registration and shutdown live.
						select {
						case <-feedCtx.Done():
							stop()
							return
						case <-time.After(retryDelay(admission)):
						}
					}
				case err, ok := <-errs:
					if !ok {
						errs = nil
						continue
					}
					if err != nil {
						e.Service().ReportError(err)
					}
					// The SDK closes feed after errs; drain messages already
					// decoded before reconnecting, even after a stream error.
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
	input := inbox.NewMessage(inbox.OriginPeer, "user", formatIOAMessage(msg))
	input.Interrupt, _ = msg.Meta["interrupt"].(bool)
	input.Message.Id = msg.ID
	input.Meta = map[string]any{"sender": msg.Sender, "message_id": msg.ID, "source_session_id": msg.Meta["source_session_id"], "target_session_id": target, "space": msg.SpaceID, "task_id": msg.Meta["task_id"], "kind": msg.Meta["kind"]}
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
	} else if e.options.Receive == nil {
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
	if (target != "" && e.options.LoadCursor == nil || e.options.Receive == nil) && (route == nil || route.deliver == nil) {
		e.mu.Unlock()
		return fmt.Errorf("no active IOA receiver for session %q", target)
	}
	var deliver func(context.Context, inbox.Message) error
	if receive := e.options.Receive; receive != nil {
		// Explicit addressing is validated above without starting a model turn.
		// Unaddressed messages are selected by the router, not a default route.
		// Admission and deduplication share the gate; failure stays retryable.
		if err := receive(ctx, input); err != nil {
			e.mu.Unlock()
			return err
		}
	} else {
		deliver = route.deliver
	}
	e.seen[msg.ID] = true
	e.recent = append(e.recent, msg.ID)
	if len(e.recent) > 1024 {
		delete(e.seen, e.recent[0])
		e.recent = e.recent[1:]
	}
	e.mu.Unlock()
	if deliver == nil {
		return nil
	}
	return deliver(ctx, input)
}

// Subscribe before replaying; the persisted cursor only advances through
// historical reads in source order. Live delivery never jumps over a gap.
func (e *CollaborationExtension) replay(ctx context.Context, space string) error {
	if e.options.LoadCursor == nil || e.options.SaveCursor == nil {
		return nil
	}
	key := "ioa:" + e.service.Client().NodeID() + ":" + space
	cursor, err := e.options.LoadCursor(key)
	if err != nil {
		return err
	}
	for {
		// This SDK returns the LAST page for an empty cursor. An initial full
		// read is required to establish an oldest-first cursor without gaps.
		limit := 256
		if cursor == "" {
			limit = 0
		}
		messages, err := e.service.ReadPublic(ctx, space, protocols.ReadOptions{After: cursor, Limit: limit, All: true})
		if err != nil {
			var protocolErr *protocols.Error
			if cursor != "" && errors.As(err, &protocolErr) && protocolErr.Status == http.StatusUnprocessableEntity && strings.HasPrefix(protocolErr.Detail, "after:") {
				e.Service().ReportError(fmt.Errorf("IOA cursor no longer exists; replaying retained source history: %w", err))
				if err := e.options.SaveCursor(key, ""); err != nil {
					return err
				}
				cursor = ""
				continue
			}
			return err
		}
		if len(messages) == 0 {
			return nil
		}
		before := cursor
		for _, message := range messages {
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = e.deliver(ctx, message); err != nil {
				return err
			}
			if err = e.options.SaveCursor(key, message.ID); err != nil {
				return err
			}
			cursor = message.ID
		}
		if cursor == before {
			return fmt.Errorf("IOA replay cursor made no progress")
		}
		if limit == 0 || len(messages) < limit {
			return nil
		}
	}
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
