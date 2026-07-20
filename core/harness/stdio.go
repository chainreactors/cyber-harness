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

func consumeAgentStream(input io.Reader, taskID string, monitor *Monitor) (string, []aop.Event, error) {
	var output string
	var events []aop.Event
	decoder := json.NewDecoder(input)
	terminal := false

	for {
		var msg webproto.Message
		if err := decoder.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				if !terminal {
					return output, events, fmt.Errorf("webproto stream ended without a terminal frame")
				}
				return output, events, nil
			}
			return output, events, fmt.Errorf("decode webproto frame: %w", err)
		}
		if terminal {
			return output, events, fmt.Errorf("received %q after terminal frame", msg.Type)
		}
		if msg.TaskID != taskID {
			return output, events, fmt.Errorf("webproto task_id = %q, want %q", msg.TaskID, taskID)
		}

		switch {
		case msg.Type == "aop":
			event, err := decodeAOPFrame(msg)
			if err != nil {
				return output, events, err
			}
			events = append(events, event)
			if event.Type == aop.TypeText {
				var data aop.TextData
				if json.Unmarshal(event.Data, &data) == nil && !data.Delta && data.Channel != aop.TextChannelReasoning && data.Role != "user" {
					output = data.Content
				}
			}
			if monitor != nil {
				monitor.renderEvent(event)
			}

		case msg.Type == "complete":
			terminal = true

		case msg.Type == "error":
			if strings.TrimSpace(msg.Data) == "" {
				return output, events, fmt.Errorf("webproto error frame has empty data")
			}
			return output, events, fmt.Errorf("agent error: %s", msg.Data)

		default:
			return output, events, fmt.Errorf("unsupported webproto frame type %q", msg.Type)
		}
	}
}

func decodeAOPFrame(msg webproto.Message) (aop.Event, error) {
	var event aop.Event
	if err := json.Unmarshal(msg.Payload, &event); err != nil {
		return event, fmt.Errorf("decode %s payload: %w", msg.Type, err)
	}
	if !event.Valid() {
		return event, fmt.Errorf("invalid AOP envelope")
	}
	return event, nil
}
