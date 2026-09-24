package console

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	agentsession "github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// RunTask owns static presentation and its event subscription. Runtime only
// executes the session and publishes events; Console owns presentation.
// finish, when supplied, runs once after session closure and before final output.
// It returns the final error, preserving any execution error it receives.
func RunTask(ctx context.Context, rt *agentsession.Runtime, option *cfg.Option, sessionID, label, display string, input agentsession.RunInput, finish func(error) error) (err error) {
	format := "text"
	if option != nil && strings.TrimSpace(option.OutputFormat) != "" {
		format = strings.ToLower(strings.TrimSpace(option.OutputFormat))
	}
	var (
		textOutput    *AgentOutput
		machineOutput *machineOutput
	)
	if format == "text" {
		textOutput = NewStaticAgentOutput(option)
	} else {
		machineOutput = newMachineOutput(os.Stdout, format)
	}
	handle := func(event *aop.Event) {
		if event == nil || isSessionBootstrapEvent(event) {
			return
		}
		if textOutput != nil {
			textOutput.HandleEvent(event)
		} else {
			machineOutput.HandleEvent(event)
		}
	}
	selector := &taskEventSelector{deliver: handle}
	unsubscribe := rt.Observe(selector)
	var session *agentsession.Session
	defer func() {
		if session != nil {
			reason := agentsession.SessionCloseCompleted
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				reason = agentsession.SessionCloseCanceled
			} else if err != nil {
				reason = agentsession.SessionCloseError
			}
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			err = errors.Join(err, rt.CloseSession(closeCtx, session.ID(), reason))
			cancel()
		}
		if finish != nil {
			err = finish(err)
		}
		if err != nil {
			selector.Bind(sessionID)
			rt.Publish(&aop.Event{SessionId: sessionID, Emitter: label, Payload: &aop.Event_Error{
				Error: &aop.ProtocolError{Code: "execution_error", Message: err.Error()},
			}})
		}
		if unsubscribe != nil {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			err = errors.Join(err, unsubscribe.Close(closeCtx))
			cancel()
		}
		if textOutput != nil {
			textOutput.Close()
		} else {
			machineOutput.SetError(err)
			err = errors.Join(err, machineOutput.Close())
		}
	}()
	if unsubscribe == nil {
		return errors.New("agent event stream is unavailable")
	}

	session, err = rt.OpenSession(ctx, agentsession.SessionOptions{ID: sessionID})
	if err != nil {
		return err
	}
	sessionID = session.ID()
	selector.Bind(sessionID)
	if textOutput != nil {
		textOutput.Start(label, display)
	}
	run, err := session.Run(ctx, input)
	if err == nil {
		_, err = run.Wait()
	}
	return err
}

// taskEventSelector subscribes before OpenSession so stream-json includes the
// session-start boundary. OpenSession may choose a continuation ID while
// resuming, so events are held until the actual ID is known.
type taskEventSelector struct {
	mu        sync.Mutex
	sessionID string
	pending   []*aop.Event
	deliver   func(*aop.Event)
}

func (s *taskEventSelector) ObserveEvent(event *aop.Event) {
	if s == nil || event == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID == "" {
		// OpenSession emits SessionStarted synchronously before returning. No
		// other session's traffic needs buffering while its actual continuation
		// ID is unresolved.
		if event.GetSessionStarted() != nil {
			s.pending = append(s.pending, event)
		}
		return
	}
	if event.SessionId == s.sessionID {
		s.deliver(event)
	}
}

func (s *taskEventSelector) Bind(sessionID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = sessionID
	for _, event := range s.pending {
		if event != nil && event.SessionId == sessionID {
			s.deliver(event)
		}
	}
	s.pending = nil
}
