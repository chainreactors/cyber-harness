package toolnode_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	toolnode "github.com/chainreactors/cyber/pkg/node/tool"
	"github.com/gorilla/websocket"
)

const (
	testDisplacedEnrollmentCode = "TEST_INSTANCE_DISPLACED"
	testDisplacedCloseCode      = 4001
)

func connectionTestConfig(serverURL string) toolnode.Config {
	return toolnode.Config{
		ServerURL: serverURL, WSPath: "/runner", ID: "node", JSON: true,
		Executor: coretool.EmptyExecutor(), Events: coreevents.New(),
		Logger: telemetry.NopLogger(), Dialer: websocket.DefaultDialer,
	}
}

func TestConnectionIdentityBindsHelloAndAcceptedNamespaces(t *testing.T) {
	type registration struct {
		instanceID string
		nonce      string
		protocol   float64
	}
	registrations := make(chan registration, 2)
	opened := make(chan toolnode.Identity, 2)
	serverErr := make(chan error, 2)
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		helloEnvelope, message, err := receive(conn, true)
		if err != nil {
			serverErr <- err
			return
		}
		hello, ok := message.(*aop.ProtocolMessage)
		if !ok || hello.GetAgentHello() == nil {
			serverErr <- fmt.Errorf("expected agent hello")
			return
		}
		fields := hello.GetAgentHello().GetRuntime().GetMetadata().GetFields()
		registrations <- registration{
			instanceID: fields["instance_id"].GetStringValue(),
			nonce:      fields["test_nonce"].GetStringValue(),
			protocol:   fields["test_protocol"].GetNumberValue(),
		}
		if err := send(conn, true, aop.Reply(helloEnvelope.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{}}})); err != nil {
			serverErr <- err
			return
		}
		if connections.Add(1) > 1 {
			_, _, _ = conn.ReadMessage()
		}
	}))
	defer server.Close()

	metadata := map[string]any{"instance_id": "configured", "test_nonce": "configured", "test_protocol": 0}
	cfg := connectionTestConfig(server.URL)
	cfg.Metadata = metadata
	cfg.ConnectionMetadata = func(identity toolnode.Identity) map[string]any {
		return map[string]any{"instance_id": "overridden", "test_nonce": identity.Nonce, "test_protocol": 1}
	}
	cfg.OpenNamespaces = func(mux *aop.NamespaceMux, calls *toolnode.CallHandler, identity toolnode.Identity) (toolnode.NamespaceSession, error) {
		if mux == nil || calls == nil {
			return nil, fmt.Errorf("connection owners are required")
		}
		opened <- identity
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- toolnode.Run(ctx, cfg) }()

	var gotRegistrations []registration
	var gotOpened []toolnode.Identity
	for len(gotRegistrations) < 2 || len(gotOpened) < 2 {
		select {
		case value := <-registrations:
			gotRegistrations = append(gotRegistrations, value)
		case value := <-opened:
			gotOpened = append(gotOpened, value)
		case err := <-serverErr:
			t.Fatal(err)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for two accepted connections")
		}
	}
	for i := range gotRegistrations {
		registered, session := gotRegistrations[i], gotOpened[i]
		if registered.instanceID == "" || registered.instanceID != session.InstanceID {
			t.Fatalf("instance identity differs at connection %d: hello=%+v session=%+v", i, registered, session)
		}
		if registered.nonce == "" || registered.nonce != session.Nonce || registered.protocol != 1 {
			t.Fatalf("connection identity differs at connection %d: hello=%+v session=%+v", i, registered, session)
		}
	}
	if gotRegistrations[0].instanceID != gotRegistrations[1].instanceID {
		t.Fatalf("process identity changed across reconnect: %+v", gotRegistrations)
	}
	if gotRegistrations[0].nonce == gotRegistrations[1].nonce {
		t.Fatalf("connection nonce was reused: %+v", gotRegistrations)
	}
	if metadata["instance_id"] != "configured" || metadata["test_nonce"] != "configured" || metadata["test_protocol"] != 0 {
		t.Fatalf("configured metadata was mutated: %+v", metadata)
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
}

func connectionEnrollmentServer(t *testing.T, dials *atomic.Int32, answer func(*websocket.Conn, *aop.Envelope) error) (*httptest.Server, <-chan error) {
	t.Helper()
	serverErr := make(chan error, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		dials.Add(1)
		conn, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		hello, _, err := receive(conn, true)
		if err != nil {
			serverErr <- err
			return
		}
		serverErr <- answer(conn, hello)
	}))
	t.Cleanup(server.Close)
	return server, serverErr
}

func terminalDisplacementPolicy(err error) bool {
	var enrollment *toolnode.EnrollmentError
	if errors.As(err, &enrollment) && enrollment.Code == testDisplacedEnrollmentCode {
		return false
	}
	var closeErr *websocket.CloseError
	return !errors.As(err, &closeErr) || closeErr.Code != testDisplacedCloseCode
}

func TestDisplacedEnrollmentReturnsCodeWithoutOpeningOrRetrying(t *testing.T) {
	var dials, opened atomic.Int32
	server, serverErr := connectionEnrollmentServer(t, &dials, func(conn *websocket.Conn, hello *aop.Envelope) error {
		return send(conn, true, aop.Reply(hello.Id, aop.NewProtocolError(testDisplacedEnrollmentCode, "displaced by a newer process")))
	})
	cfg := connectionTestConfig(server.URL)
	cfg.ShouldRetry = terminalDisplacementPolicy
	cfg.OpenNamespaces = func(*aop.NamespaceMux, *toolnode.CallHandler, toolnode.Identity) (toolnode.NamespaceSession, error) {
		opened.Add(1)
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := toolnode.Run(ctx, cfg)
	var enrollment *toolnode.EnrollmentError
	if !errors.As(err, &enrollment) || enrollment.Code != testDisplacedEnrollmentCode {
		t.Fatalf("enrollment error = %#v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	if dials.Load() != 1 || opened.Load() != 0 {
		t.Fatalf("dials=%d namespace opens=%d", dials.Load(), opened.Load())
	}
}

func TestDisplacedCloseDoesNotRetry(t *testing.T) {
	var dials atomic.Int32
	server, serverErr := connectionEnrollmentServer(t, &dials, func(conn *websocket.Conn, hello *aop.Envelope) error {
		if err := send(conn, true, aop.Reply(hello.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{}}})); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
		return conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(testDisplacedCloseCode, "displaced by a newer process"),
			time.Now().Add(time.Second))
	})
	cfg := connectionTestConfig(server.URL)
	cfg.ShouldRetry = terminalDisplacementPolicy
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := toolnode.Run(ctx, cfg)
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != testDisplacedCloseCode {
		t.Fatalf("close error = %#v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	if dials.Load() != 1 {
		t.Fatalf("dials=%d", dials.Load())
	}
}

type testNamespaceSession struct {
	canceled chan string
	closed   chan struct{}
	once     sync.Once
}

func (s *testNamespaceSession) Cancel(id string) { s.canceled <- id }
func (s *testNamespaceSession) Close(context.Context) error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func TestRawCancelOperationReachesNamespaceSession(t *testing.T) {
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		hello, _, err := receive(conn, true)
		if err != nil {
			serverErr <- err
			return
		}
		if err := send(conn, true, aop.Reply(hello.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{}}})); err != nil {
			serverErr <- err
			return
		}
		cancel := &aop.CancelOperation{TargetId: "raw-operation"}
		serverErr <- send(conn, true, aop.MustWrap("cancel-raw", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelOperation{CancelOperation: cancel}}))
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	session := &testNamespaceSession{canceled: make(chan string, 1), closed: make(chan struct{})}
	cfg := connectionTestConfig(server.URL)
	cfg.OpenNamespaces = func(*aop.NamespaceMux, *toolnode.CallHandler, toolnode.Identity) (toolnode.NamespaceSession, error) {
		return session, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- toolnode.Run(ctx, cfg) }()
	canceled := false
	for !canceled {
		select {
		case target := <-session.canceled:
			if target != "raw-operation" {
				t.Fatalf("cancel target=%q", target)
			}
			canceled = true
		case err := <-done:
			t.Fatalf("tool node stopped before namespace cancellation: %v", err)
		case err := <-serverErr:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for raw namespace cancellation")
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
	select {
	case <-session.closed:
	case <-time.After(time.Second):
		t.Fatal("namespace session was not closed")
	}
}
