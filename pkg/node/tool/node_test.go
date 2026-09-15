package toolnode_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/cmd/harness"
	"github.com/chainreactors/cyber/core/eventbus"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/node/tool"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

type testTool struct {
	started chan struct{}
	events  *coreevents.Stream
}

func (t *testTool) Name() string        { return "echo" }
func (t *testTool) Description() string { return "echo or wait" }
func (t *testTool) Definition() *tool.Definition {
	return tool.Def(t.Name(), t.Description(), struct {
		Value string `json:"value"`
	}{})
}
func (t *testTool) Execute(ctx context.Context, arguments string) (*tool.Result, error) {
	if arguments == `{"value":"wait"}` {
		invocation := operation.InvocationFromContext(ctx)
		if invocation.Progress != nil {
			invocation.Progress([]byte("working\n"))
		}
		if t.events != nil {
			artifact := &toolpb.Artifact{Tool: t.Name(), Kind: "test", Data: []byte("observed"), ResultId: "artifact-1", Timestamp: timestamppb.Now()}
			extension, err := anypb.New(artifact)
			if err != nil {
				return nil, err
			}
			event := &aop.Event{Emitter: invocation.Emitter, Payload: &aop.Event_Extension{Extension: extension}}
			if err := aop.SetTypedExtension(event, operation.Correlation(ctx)); err != nil {
				return nil, err
			}
			t.events.Publish(event)
		}
		close(t.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return tool.TextResult(arguments), nil
}

func send(conn *websocket.Conn, json bool, envelope *aop.Envelope) error {
	frame := websocket.BinaryMessage
	var data []byte
	var err error
	if json {
		frame = websocket.TextMessage
		data, err = protojson.Marshal(envelope)
	} else {
		data, err = proto.Marshal(envelope)
	}
	if err != nil {
		return err
	}
	return conn.WriteMessage(frame, data)
}

func receive(conn *websocket.Conn, json bool) (*aop.Envelope, proto.Message, error) {
	_, data, err := conn.ReadMessage()
	if err != nil {
		return nil, nil, err
	}
	envelope := new(aop.Envelope)
	if json {
		err = protojson.Unmarshal(data, envelope)
	} else {
		err = proto.Unmarshal(data, envelope)
	}
	if err != nil {
		return nil, nil, err
	}
	message, err := aop.Unwrap(envelope)
	return envelope, message, err
}

func TestWireCallCancellationAndStableReconnectIdentity(t *testing.T) {
	for _, jsonFrames := range []bool{false, true} {
		name := "protobuf"
		if jsonFrames {
			name = "json"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			events := coreevents.New()
			progress := eventbus.New[*toolpb.Progress]()
			r := harness.Tools(t, &testTool{started: started, events: events})
			instanceIDs := make(chan string, 2)
			result := make(chan *aop.ToolResult, 1)
			extras := make(chan string, 2)
			serverErr := make(chan error, 2)
			var connections atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Header.Get("Authorization") != "Bearer secret" {
					serverErr <- errors.New("missing bearer token")
					return
				}
				conn, err := upgrader.Upgrade(w, request, nil)
				if err != nil {
					serverErr <- err
					return
				}
				defer conn.Close()
				helloEnvelope, message, err := receive(conn, jsonFrames)
				hello, ok := message.(*aop.ProtocolMessage)
				if err != nil || !ok || hello.GetAgentHello() == nil {
					serverErr <- errors.New("missing hello")
					return
				}
				registration := hello.GetAgentHello()
				if len(registration.Capabilities) != 1 || registration.Capabilities[0] != "tool" || len(registration.Tools) != 1 {
					serverErr <- errors.New("unexpected advertised surface")
					return
				}
				instanceIDs <- registration.Runtime.Metadata.Fields["instance_id"].GetStringValue()
				if err := send(conn, jsonFrames, aop.Reply(helloEnvelope.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: registration.NodeId}}})); err != nil {
					serverErr <- err
					return
				}
				if connections.Add(1) == 1 {
					return
				}
				arguments, _ := aop.JSONValue(map[string]any{"value": "wait"})
				call := &toolpb.Call{Call: &aop.ToolCall{Id: "call-1", Name: "echo", Arguments: arguments}}
				if err := send(conn, jsonFrames, aop.MustWrap("call-1", "", &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{Call: call}})); err != nil {
					serverErr <- err
					return
				}
				<-started
				cancel := &aop.CancelOperation{TargetId: "call-1"}
				if err := send(conn, jsonFrames, aop.MustWrap("cancel-1", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelOperation{CancelOperation: cancel}})); err != nil {
					serverErr <- err
					return
				}
				for {
					_, response, err := receive(conn, jsonFrames)
					if err != nil {
						serverErr <- err
						return
					}
					switch value := response.(type) {
					case *toolpb.ProtocolMessage:
						if item := value.GetProgress(); item != nil {
							if item.GetCallId() != "call-1" || item.GetText() != "working" {
								serverErr <- errors.New("invalid progress correlation")
								return
							}
							extras <- "progress"
						}
					case *aop.ProtocolMessage:
						if terminal := value.GetEvent().GetToolResult(); terminal != nil {
							result <- terminal
							return
						}
						if extension := value.GetEvent().GetExtension(); extension != nil {
							artifact := new(toolpb.Artifact)
							ref := new(operationpb.Ref)
							found, refErr := aop.FindTypedExtension(value.GetEvent(), ref)
							if extension.UnmarshalTo(artifact) != nil || refErr != nil || !found || ref.GetCallId() != "call-1" || string(artifact.GetData()) != "observed" {
								serverErr <- errors.New("invalid artifact correlation")
								return
							}
							extras <- "artifact"
						}
					}
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- toolnode.Run(ctx, toolnode.Config{
					ServerURL: server.URL, WSPath: "/runner", ID: "runner-1", Token: "secret",
					Version: "test", JSON: jsonFrames, Executor: r, Events: events, Progress: progress, Logger: telemetry.NopLogger(),
				})
			}()
			var ids []string
			for len(ids) != 2 {
				select {
				case id := <-instanceIDs:
					ids = append(ids, id)
				case err := <-serverErr:
					t.Fatal(err)
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for reconnect")
				}
			}
			if ids[0] == "" || ids[0] != ids[1] {
				t.Fatalf("instance identity changed across reconnect: %q", ids)
			}
			select {
			case terminal := <-result:
				if !terminal.IsError || terminal.CallId != "call-1" || terminal.Name != "echo" || !strings.Contains(tool.ResultText(terminal), "canceled") {
					t.Fatalf("canceled result: %+v", terminal)
				}
			case err := <-serverErr:
				t.Fatal(err)
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for canceled result")
			}
			seen := map[string]bool{}
			for len(seen) != 2 {
				select {
				case kind := <-extras:
					seen[kind] = true
				case err := <-serverErr:
					t.Fatal(err)
				case <-time.After(5 * time.Second):
					t.Fatalf("timed out waiting for progress and artifact: %v", seen)
				}
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Run result: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("tool node did not stop")
			}
		})
	}
}
