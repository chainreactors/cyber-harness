package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/core/operation"
	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/proto"
)

// ForTask inherits execution settings but starts an independent task. It
// does not create a Runtime Session; the caller owns execution and cancellation.
func (c Config) ForTask(name, parentToolCallID string, detail *types.DelegationDetail) Config {
	c.ParentSessionID = c.SessionID
	c.ParentToolCallID = parentToolCallID
	c.AgentName = name
	c.Delegation = detail
	c.SessionID, c.TurnID = "", ""
	c.Messages, c.Inbox, c.LoopScheduler = nil, nil, nil
	c.MessageCounter = 0
	c.emitter = nil
	return c
}

// RunTask executes a foreground task without a Runtime Session. Its caller
// owns the task; the child has an isolated inbox and an AOP delegation trace.
func RunTask(ctx context.Context, cfg Config, detail *types.DelegationDetail) (result *Result, err error) {
	if detail == nil {
		return nil, fmt.Errorf("task is required")
	}
	detail = proto.CloneOf(detail)
	if detail.AgentName == "" {
		detail.AgentName = cfg.AgentName
	}
	cfg.AgentName = detail.AgentName
	if strings.TrimSpace(detail.Task) == "" {
		return nil, fmt.Errorf("task is required")
	}
	invocation := operation.InvocationFromContext(ctx)
	if cfg.SessionID == "" {
		cfg.SessionID = invocation.SessionID
	}
	detail.RunMode, detail.ContextMode = types.DelegationRunForeground, types.DelegationContextFresh
	cfg = cfg.ForTask(detail.AgentName, invocation.CallID, detail)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if cfg.Lifetime != nil {
		stop := context.AfterFunc(cfg.Lifetime, cancel)
		defer stop()
		if cfg.Lifetime.Err() != nil {
			cancel()
		}
	}
	if err := runCtx.Err(); err != nil {
		return nil, err
	}
	cfg.Lifetime = runCtx
	sub := NewAgent(cfg)
	defer sub.Cfg.Inbox.Close()
	em := sub.Cfg.emitter.turn(randomID())
	em.sessionStart(sub.Cfg.Model)
	em.turnStart()
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("task %q panicked: %v", detail.AgentName, failure)
		}
		stop := StopReasonCompleted
		if result != nil && result.Stop != "" {
			stop = result.Stop
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			stop = StopReasonCanceled
		} else if err != nil {
			stop = StopReasonError
		}
		if result == nil {
			em.turnEnd(stop, nil, 0, err)
		} else {
			em.turnEnd(stop, result.TotalUsage, result.ContextTokens, err)
		}
		em.sessionEnd(string(stop))
	}()
	return sub.Run(runCtx, TextInput(detail.Task), WithTurnID(em.turnID))
}
