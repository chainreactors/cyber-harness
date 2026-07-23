//go:build e2e

package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// messageText flattens the text parts of a complete AOP message.
func messageText(data aop.MessageData) string {
	var sb strings.Builder
	for _, part := range data.Parts {
		if part.Type != aop.PartText || part.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(part.Text)
	}
	return sb.String()
}

func consumeAgentStream(input io.Reader, monitor *Monitor) (string, []aop.Event, error) {
	var output string
	var events []aop.Event
	decoder := json.NewDecoder(input)
	sessionID := ""
	runID := ""
	var agentErr error

	for {
		var message webproto.Message
		if err := decoder.Decode(&message); err != nil {
			if errors.Is(err, io.EOF) {
				if sessionID == "" {
					return output, events, fmt.Errorf("stdio stream ended without session.opened")
				}
				return output, events, fmt.Errorf("stdio stream ended without run turn.end")
			}
			return output, events, fmt.Errorf("decode stdio frame: %w", err)
		}
		switch message.Type {
		case webproto.TypeSessionOpened:
			var data webproto.SessionLifecyclePayload
			if err := json.Unmarshal(message.Payload, &data); err != nil {
				return output, events, fmt.Errorf("decode session.opened: %w", err)
			}
			if data.SessionID == "" {
				return output, events, fmt.Errorf("session.opened has empty session_id")
			}
			if sessionID == "" {
				sessionID = data.SessionID
			} else if sessionID != data.SessionID {
				return output, events, fmt.Errorf("unexpected session.opened for %q", data.SessionID)
			}

		case webproto.TypeError:
			var data webproto.ErrorPayload
			if err := json.Unmarshal(message.Payload, &data); err != nil {
				return output, events, fmt.Errorf("decode error frame: %w", err)
			}
			if strings.TrimSpace(data.Message) == "" {
				return output, events, fmt.Errorf("stdio error frame has empty message")
			}
			return output, events, fmt.Errorf("agent error: %s", data.Message)

		case webproto.TypeAOP:
			var event aop.Event
			if err := json.Unmarshal(message.Payload, &event); err != nil {
				return output, events, fmt.Errorf("decode AOP payload: %w", err)
			}
			if !event.Valid() {
				return output, events, fmt.Errorf("invalid AOP envelope")
			}
			events = append(events, event)
			if monitor != nil {
				monitor.renderEvent(event)
			}
			if sessionID == "" && event.Type == aop.TypeSessionStart {
				sessionID = event.SessionID
			}
			if event.SessionID != sessionID {
				continue
			}
			if runID == "" && event.Type == aop.TypeTurnStart {
				runID = event.TurnID
			}
			if runID != "" && event.TurnID != "" && event.TurnID != runID {
				continue
			}
			switch event.Type {
			case aop.TypeMessage:
				var data aop.MessageData
				if json.Unmarshal(event.Data, &data) == nil && data.Role != "user" {
					if text := messageText(data); text != "" {
						output = text
					}
				}
			case aop.TypeError:
				var data aop.ErrorData
				if json.Unmarshal(event.Data, &data) != nil || strings.TrimSpace(data.Message) == "" {
					return output, events, fmt.Errorf("run AOP error has empty message")
				}
				agentErr = fmt.Errorf("agent error: %s", data.Message)
			case aop.TypeTurnEnd:
				var data aop.TurnEndData
				if err := json.Unmarshal(event.Data, &data); err != nil {
					return output, events, fmt.Errorf("decode turn.end: %w", err)
				}
				if agentErr != nil {
					return output, events, agentErr
				}
				if data.Stop == "error" || data.Error != "" {
					return output, events, fmt.Errorf("agent error: %s", strings.TrimSpace(data.Error))
				}
				return output, events, nil
			}
		}
	}
}
