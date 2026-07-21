//go:build e2e

package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/chainreactors/aiscan/pkg/aop"
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
	rootSessionID := ""
	var agentErr error

	for {
		var event aop.Event
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				if rootSessionID == "" {
					return output, events, fmt.Errorf("AOP stream ended without a root session")
				}
				return output, events, fmt.Errorf("AOP stream ended without root session.end")
			}
			return output, events, fmt.Errorf("decode AOP event: %w", err)
		}
		if !event.Valid() {
			return output, events, fmt.Errorf("invalid AOP envelope")
		}
		events = append(events, event)
		if monitor != nil {
			monitor.renderEvent(event)
		}

		if event.Type == aop.TypeSessionStart {
			var data aop.SessionStartData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return output, events, fmt.Errorf("decode session.start: %w", err)
			}
			if data.ParentSessionID == "" && rootSessionID == "" {
				rootSessionID = event.SessionID
			}
		}

		if rootSessionID == "" {
			if event.Type == aop.TypeError {
				var data aop.ErrorData
				if json.Unmarshal(event.Data, &data) == nil && strings.TrimSpace(data.Message) != "" {
					rootSessionID = event.SessionID
					agentErr = fmt.Errorf("agent error: %s", data.Message)
				}
			}
			if rootSessionID == "" {
				continue
			}
		}

		if event.SessionID != rootSessionID {
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
				return output, events, fmt.Errorf("root AOP error has empty message")
			}
			agentErr = fmt.Errorf("agent error: %s", data.Message)
		case aop.TypeSessionEnd:
			var data aop.SessionEndData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return output, events, fmt.Errorf("decode session.end: %w", err)
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
