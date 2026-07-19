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

func consumeAgentStream(input io.Reader, taskID string, monitor *Monitor) (string, []AgentEvent, error) {
	var output string
	var events []AgentEvent
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
		case strings.HasPrefix(msg.Type, "aop."):
			event, err := decodeAOPFrame(msg)
			if err != nil {
				return output, events, err
			}
			flattened, err := flattenAOPEvent(event)
			if err != nil {
				return output, events, err
			}
			events = append(events, flattened)
			if monitor != nil {
				monitor.renderEvent(flattened)
			}

		case msg.Type == "complete":
			output = msg.Data
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
	if event.V != aop.Version {
		return event, fmt.Errorf("unsupported AOP version %d", event.V)
	}
	wantType := strings.TrimPrefix(msg.Type, "aop.")
	if event.Type != wantType {
		return event, fmt.Errorf("webproto type %q contains AOP event %q", msg.Type, event.Type)
	}
	return event, nil
}
