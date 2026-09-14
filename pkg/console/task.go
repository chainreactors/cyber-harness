package console

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	agentext "github.com/chainreactors/aiscan/pkg/exts/agent"
)

// RunTask owns static presentation and its event subscription. Runtime only
// executes the session and publishes events; Console owns presentation.
func RunTask(ctx context.Context, rt *agentext.Runtime, option *cfg.Option, sessionID, label, display string, input agentext.RunInput) error {
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
	if unsubscribe == nil {
		return errors.New("agent event stream is unavailable")
	}

	session, err := rt.OpenSession(ctx, agentext.SessionOptions{ID: sessionID})
	if err != nil {
		unsubscribe.Cancel()
		if textOutput != nil {
			textOutput.Close()
		} else {
			machineOutput.SetError(err)
			err = errors.Join(err, machineOutput.Close())
		}
		return err
	}
	selector.Bind(session.ID())
	if textOutput != nil {
		textOutput.Start(label, display)
	}
	run, err := session.Run(ctx, input)
	if err == nil {
		_, err = run.Wait()
	}
	reason := agentext.SessionCloseCompleted
	if errors.Is(err, context.Canceled) {
		reason = agentext.SessionCloseCanceled
	} else if err != nil {
		reason = agentext.SessionCloseError
	}
	closeErr := rt.CloseSession(context.Background(), session.ID(), reason)
	subErr := unsubscribe.Close(context.Background())
	if textOutput != nil {
		textOutput.Close()
	} else {
		machineOutput.SetError(errors.Join(err, closeErr, subErr))
		closeErr = errors.Join(closeErr, machineOutput.Close())
	}
	return errors.Join(err, closeErr, subErr)
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
