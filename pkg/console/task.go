package console

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	agentsession "github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/pkg/config"
	outputpkg "github.com/chainreactors/cyber/pkg/output"
	"golang.org/x/term"
)

// TaskValidation lets a product check deliverables before the session closes.
// Failed checks are returned to the same model context for bounded repair.
type TaskValidation struct {
	Check             func(context.Context) error
	MaxRepairRounds   int
	RepairInstruction string
}

// TaskOptions configures one-shot delivery. Nil writers use the process streams.
type TaskOptions struct {
	Stdout, Stderr io.Writer
	Validation     TaskValidation
}

// TaskValidationError means execution finished but its deliverable is incomplete.
type TaskValidationError struct{ Err error }

func (e *TaskValidationError) Error() string { return e.Err.Error() }
func (e *TaskValidationError) Unwrap() error { return e.Err }

// RunTask owns static presentation and its event subscription. Runtime only
// executes the session and publishes events; Console owns presentation.
// finish, when supplied, runs once after session closure and before final output.
// It returns the final error, preserving any execution error it receives.
func RunTask(ctx context.Context, rt *agentsession.Runtime, option *cfg.Option, sessionID, label, display string, input agentsession.RunInput, finish func(error) error, options ...TaskOptions) (err error) {
	var settings TaskOptions
	if len(options) > 0 {
		settings = options[0]
	}
	if settings.Stdout == nil {
		settings.Stdout = os.Stdout
	}
	if settings.Stderr == nil {
		settings.Stderr = os.Stderr
	}
	// A presentation label such as task/scanner must not create an empty session
	// when the runtime has restored the primary session's history.
	if option != nil && option.Resume != "" {
		sessionID = rt.PrimarySessionID()
	}
	format := "text"
	if option != nil && strings.TrimSpace(option.OutputFormat) != "" {
		format = strings.ToLower(strings.TrimSpace(option.OutputFormat))
	}
	var (
		textOutput             *AgentOutput
		machineOutput          *outputpkg.EventWriter
		textStdout, textStderr *errorWriter
	)
	if format == "text" {
		isTerminal := func(w io.Writer) bool { file, ok := w.(*os.File); return ok && term.IsTerminal(int(file.Fd())) }
		textStdout, textStderr = &errorWriter{writer: settings.Stdout}, &errorWriter{writer: settings.Stderr}
		textOutput = newAgentOutput(option, textStdout, textStderr, isTerminal(settings.Stdout), isTerminal(settings.Stderr), ModeStatic)
	} else {
		machineOutput = outputpkg.NewEventWriter(settings.Stdout, format)
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
	unsubscribe := rt.Observe(selector.observe)
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
			err = errors.Join(err, textStdout.Err(), textStderr.Err())
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
	validation := settings.Validation
	for attempt := 0; ; attempt++ {
		run, runErr := session.Run(ctx, input)
		if runErr != nil {
			return runErr
		}
		result, runErr := run.Wait()
		if runErr != nil {
			return runErr
		}
		if validation.Check == nil || result == nil || result.Stop != agenthooks.StopReasonCompleted {
			return nil
		}
		checkErr := validation.Check(ctx)
		if checkErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt >= validation.MaxRepairRounds {
			return &TaskValidationError{Err: checkErr}
		}
		input = agentsession.RunInput{Content: []*aop.Content{aop.Text(fmt.Sprintf(
			"Deliverable validation failed (repair %d/%d):\n%s\n\n%s",
			attempt+1, validation.MaxRepairRounds, checkErr, validation.RepairInstruction))}}
	}
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

func (s *taskEventSelector) observe(event *aop.Event) {
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
