package guardrail

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"

	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
	"github.com/chainreactors/cyber/pkg/console/api"
	"google.golang.org/protobuf/proto"
)

func TestExtensionInstallsBoundaryAndDirectApprovalAdapters(t *testing.T) {
	for _, adapter := range []string{"console", "protocol"} {
		t.Run(adapter, func(t *testing.T) {
			stream := events.New()
			registry := hooks.New()
			var runtime *Runtime
			set, err := extension.New(extension.Provided[*events.Stream](stream), extension.Provided[*hooks.Registry](registry), New(Config{Mode: ModeSafe, ReviewTimeout: "1s"}), extension.Func{LoadFunc: func(scope *extension.Scope) error {
				var err error
				runtime, err = extension.Use[*Runtime](scope)
				return err
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err = set.Load(t.Context()); err != nil {
				t.Fatal(err)
			}
			defer set.Close(t.Context())
			runtime.check, runtime.confirm = func(context.Context, toolhooks.CallEvent) (*Decision, error) {
				return &Decision{Action: Action_ACTION_REVIEW}, nil
			}, nil
			pending := make(chan *Review, 1)
			stream.Observe(testObserver(func(event *aop.Event) {
				var review Review
				if payload := event.GetExtension(); payload != nil && payload.MessageIs(&review) && payload.UnmarshalTo(&review) == nil && review.State == ReviewState_REVIEW_STATE_PENDING {
					pending <- &review
				}
			}))
			ctx := operation.ContextWithInvocation(t.Context(), operation.Invocation{SessionID: "session"})
			done := make(chan error, 1)
			go func() {
				_, err := toolhooks.Execute(ctx, registry, "shell", "{}", func(context.Context, string) (*aop.ToolResult, error) { return &aop.ToolResult{}, nil })
				done <- err
			}()
			var review *Review
			select {
			case review = <-pending:
			case <-time.After(time.Second):
				t.Fatal("missing review")
			}
			if adapter == "console" {
				var out bytes.Buffer
				commands := consoleBindings(runtime, nil).Commands(api.View{Out: &out, SessionID: func() string { return "session" }, Command: func(string) error { t.Error("approval queued a Session command"); return nil }})
				commands[0].SetArgs([]string{"approve", review.Operation.OperationId})
				if err = commands[0].ExecuteContext(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				handler := protocolHandler(runtime, nil)
				request := &ProtocolMessage{Message: &ProtocolMessage_Resolve{Resolve: &ResolveRequest{SessionId: "other", OperationId: review.Operation.OperationId, Approve: true}}}
				var response proto.Message
				send := func(envelope *aop.Envelope) error { var err error; response, err = aop.Unwrap(envelope); return err }
				envelope := aop.MustWrap("approve", "", request)
				if err = handler(context.Background(), envelope, request, send); err != nil {
					t.Fatal(err)
				}
				if result, ok := response.(*aop.ProtocolMessage); !ok || result.GetProtocolError() == nil {
					t.Fatal("cross-session approval accepted")
				}
				request.GetResolve().SessionId = "session"
				if err = handler(ctx, envelope, request, send); err != nil {
					t.Fatal(err)
				}
				if result, ok := response.(*ProtocolMessage); !ok || result.GetResolved() == nil {
					t.Fatalf("unexpected approval response %T", response)
				}
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("approval blocked behind tool")
			}
		})
	}
}

func TestCloseCancelsPendingReview(t *testing.T) {
	stream := events.New()
	registry := hooks.New()
	e := New(Config{Mode: ModeSafe})
	set, _ := extension.New(extension.Provided[*events.Stream](stream), extension.Provided[*hooks.Registry](registry), e)
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.runtime.check, e.runtime.confirm = func(context.Context, toolhooks.CallEvent) (*Decision, error) {
		return &Decision{Action: Action_ACTION_REVIEW}, nil
	}, nil
	entered := make(chan struct{})
	stream.Observe(testObserver(func(event *aop.Event) {
		var r Review
		if payload := event.GetExtension(); payload != nil && payload.MessageIs(&r) && payload.UnmarshalTo(&r) == nil && r.State == ReviewState_REVIEW_STATE_PENDING {
			close(entered)
		}
	}))
	done := make(chan error, 1)
	go func() {
		_, err := toolhooks.Execute(t.Context(), registry, "shell", "{}", func(context.Context, string) (*aop.ToolResult, error) {
			t.Error("executed during shutdown")
			return nil, nil
		})
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := set.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, operation.ErrDenied) {
		t.Fatal(err)
	}
}

func TestInteractionModeConfiguration(t *testing.T) {
	for _, mode := range []Mode{"", ModeSafe, ModeAuto} {
		if _, err := (Config{Mode: mode}).timeout(); err != nil {
			t.Errorf("valid mode %q rejected: %v", mode, err)
		}
	}
	for _, mode := range []Mode{"off", "unknown"} {
		if _, err := (Config{Mode: mode}).timeout(); err == nil {
			t.Fatal("invalid interaction mode accepted")
		}
	}
	if _, err := (Config{Rules: Rules{OnError: "record"}}).timeout(); err == nil {
		t.Fatal("unavailable judgments cannot be configured as successful admission")
	}
}
