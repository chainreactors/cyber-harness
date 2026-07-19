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

type agentStreamResult struct {
	Output string
	Events []AgentEvent
}

func consumeAgentStream(input io.Reader, taskID string, monitor *Monitor) (agentStreamResult, error) {
	var result agentStreamResult
	decoder := json.NewDecoder(input)
	terminal := false

	for {
		var msg webproto.Message
		if err := decoder.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				if !terminal {
					return result, fmt.Errorf("webproto stream ended without a terminal frame")
				}
				return result, nil
			}
			return result, fmt.Errorf("decode webproto frame: %w", err)
		}
		if terminal {
			return result, fmt.Errorf("received %q after terminal frame", msg.Type)
		}
		if msg.TaskID != taskID {
			return result, fmt.Errorf("webproto task_id = %q, want %q", msg.TaskID, taskID)
		}

		switch {
		case strings.HasPrefix(msg.Type, "aop."):
			event, err := decodeAOPFrame(msg)
			if err != nil {
				return result, err
			}
			flattened, err := flattenAOPEvent(event)
			if err != nil {
				return result, err
			}
			result.Events = append(result.Events, flattened)
			if monitor != nil {
				monitor.renderEvent(flattened)
			}

		case msg.Type == "complete":
			result.Output = msg.Data
			terminal = true

		case msg.Type == "error":
			if strings.TrimSpace(msg.Data) == "" {
				return result, fmt.Errorf("webproto error frame has empty data")
			}
			return result, fmt.Errorf("agent error: %s", msg.Data)

		default:
			return result, fmt.Errorf("unsupported webproto frame type %q", msg.Type)
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
