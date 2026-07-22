package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/pkg/aop"
	xeval "github.com/chainreactors/aiscan/pkg/aop/x/eval"
)

type InboundKind uint8

const (
	InboundUnknown InboundKind = iota
	InboundUserMessage
	InboundToolCall
)

type Inbound struct {
	Event      aop.Event
	Kind       InboundKind
	Message    aop.MessageData
	ToolCall   aop.ToolCallData
	RunControl aop.RunControl
	Eval       xeval.Control
}

func Classify(event aop.Event) (Inbound, error) {
	in := Inbound{Event: event}
	if !event.Valid() {
		return in, fmt.Errorf("invalid AOP envelope")
	}
	if control, ok, err := aop.Ext[aop.RunControl](event, aop.NSAOP); err != nil {
		return in, err
	} else if ok {
		in.RunControl = control
	}
	if control, ok, err := xeval.Get(event); err != nil {
		return in, err
	} else if ok {
		in.Eval = control
	}
	switch event.Type {
	case aop.TypeMessage:
		data, err := aop.DecodeData[aop.MessageData](event)
		if err != nil {
			return in, err
		}
		if data.Role != "user" {
			return in, fmt.Errorf("inbound message role must be user")
		}
		in.Kind, in.Message = InboundUserMessage, data
	case aop.TypeToolCall:
		data, err := aop.DecodeData[aop.ToolCallData](event)
		if err != nil {
			return in, err
		}
		if data.ToolCallID == "" || data.ToolName == "" {
			return in, fmt.Errorf("invalid inbound tool.call")
		}
		in.Kind, in.ToolCall = InboundToolCall, data
	default:
		return in, fmt.Errorf("unsupported inbound AOP type %q", event.Type)
	}
	return in, nil
}

type EvalExecutor func(context.Context, *Agent, string, xeval.Control) (*Result, error)

type InboundDependencies struct {
	DefaultMaxTurns int
	Eval            EvalExecutor
}

func ExecuteInbound(ctx context.Context, ag *Agent, in Inbound, deps InboundDependencies) (*Result, error) {
	if in.Kind != InboundUserMessage {
		return nil, fmt.Errorf("ExecuteInbound requires a user message")
	}
	input := InputFromAOPMessage(in.Message)
	input.NoEcho = in.RunControl.NoEcho
	prompt := strings.TrimSpace(inboundMessageText(in.Message))
	if prompt == "" {
		return nil, fmt.Errorf("empty prompt")
	}
	if in.Eval.Criteria != "" {
		if deps.Eval == nil {
			return nil, fmt.Errorf("eval executor is not configured")
		}
		return deps.Eval(ctx, ag, prompt, in.Eval)
	}
	maxTurns := deps.DefaultMaxTurns
	if in.RunControl.MaxTurns > 0 {
		maxTurns = in.RunControl.MaxTurns
	}
	return ag.Run(ctx, input, WithRunMaxTurns(maxTurns))
}

func inboundMessageText(message aop.MessageData) string {
	var parts []string
	for _, part := range message.Parts {
		if part.Type == aop.PartText && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}
