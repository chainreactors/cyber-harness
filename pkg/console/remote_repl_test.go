package console

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	agentsession "github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	ptypb "github.com/chainreactors/cyber/aop/pty"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/namespaces"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	ptyext "github.com/chainreactors/cyber/pkg/exts/pty"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	"github.com/chainreactors/cyber/pkg/hosttest"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
	"github.com/chainreactors/utils/proc"
)

// loadPTYRegistry mounts the PTY extension exactly as a Profile does.
func loadPTYRegistry(t *testing.T, ctx context.Context, bash *terminaltool.BashTool) *namespaces.Registry {
	t.Helper()
	registry := namespaces.New()
	value := hosttest.Set(t, registry, ptyext.New(bash))
	if err := value.Load(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = value.Close(context.Background()) })
	return registry
}

// ptyTransport models one AOP connection. Binding the registry opens a fresh
// PTY handler for it, and cancelling its context is what a connection ending
// looks like now that the router has no teardown of its own.
type ptyTransport struct {
	t        *testing.T
	mux      *aop.NamespaceMux
	cancel   context.CancelFunc
	messages chan *ptypb.ProtocolMessage
}

func newPTYTransport(t *testing.T, ctx context.Context, registry *namespaces.Registry, buffer int) *ptyTransport {
	t.Helper()
	connectionCtx, cancel := context.WithCancel(ctx)
	transport := &ptyTransport{t: t, cancel: cancel, messages: make(chan *ptypb.ProtocolMessage, buffer)}
	transport.mux = aop.NewNamespaceMux(connectionCtx)
	if err := registry.Bind(transport.mux); err != nil {
		t.Fatalf("bind PTY namespace: %v", err)
	}
	t.Cleanup(transport.close)
	return transport
}

func (p *ptyTransport) close() {
	p.cancel()
	_ = p.mux.Close(context.Background())
}

func (p *ptyTransport) dispatch(message *ptypb.ProtocolMessage) {
	p.t.Helper()
	handled, err := p.mux.Dispatch(aop.MustWrap("envelope", "", message), func(out *aop.Envelope) error {
		reply := &ptypb.ProtocolMessage{}
		if err := out.GetPayload().UnmarshalTo(reply); err != nil {
			return err
		}
		p.messages <- reply
		return nil
	})
	if err != nil || !handled {
		p.t.Fatalf("dispatch PTY message: handled=%v err=%v", handled, err)
	}
}

func TestConsoleOwnsPersistentMainREPLWithoutProvider(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	option := &cfg.Option{REPLMode: "fast"}
	application := newTestApp(t, telemetry.NopLogger(), apppkg.Dependencies{})

	applicationSet := loadConsoleApplication(t, ctx, application)
	defer applicationSet.Close(context.Background())
	rt, err := sessionext.New(agentsession.Config{Application: application, Option: option, Logger: telemetry.NopLogger(),
		PrimarySessionID: MainREPLName,
		Loop:             agent.StandardLoop{},
	})
	if err != nil {
		t.Fatalf("runtime without provider: %v", err)
	}

	rtSet := hosttest.Set(t, rt)
	if err := rtSet.Load(ctx); err != nil {
		t.Fatal(err)
	}
	defer rtSet.Close(context.Background())

	repl, err := StartPersistent(rt.Runtime(), option, testSessionBindings(t, rt.Runtime()))
	if err != nil {
		t.Fatal(err)
	}
	defer repl.Close()
	mgr := bashManager(rt.Runtime().App().Bash)
	if mgr == nil {
		t.Fatal("pty manager unavailable")
	}

	var initial proc.Info
	for _, info := range mgr.List() {
		if info.State == proc.StateRunning && info.Kind == "repl" && info.Name == MainREPLName {
			initial = info
			break
		}
	}
	if initial.ID == "" {
		t.Fatal("main-repl was not created eagerly")
	}
	if initial.Name != MainREPLName || initial.Kind != "repl" || initial.State != proc.StateRunning {
		t.Fatalf("unexpected resident repl: %+v", initial)
	}

	registry := loadPTYRegistry(t, ctx, rt.Runtime().App().Bash)
	transport := newPTYTransport(t, ctx, registry, 64)
	messages := transport.messages
	transport.dispatch(&ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Attach{Attach: &ptypb.Attach{
		StreamId: "term-repl", SessionId: initial.ID,
	}}})
	opened := waitForPTYMessage(t, messages, time.Second, func(message *ptypb.ProtocolMessage) bool {
		if value := message.GetError(); value != nil {
			t.Fatalf("unexpected pty error: %s", value.GetMessage())
		}
		return message.GetAttached() != nil
	})
	if opened.GetAttached().GetSession().GetId() != initial.ID {
		t.Fatalf("transport created a second repl: got %s want %s", opened.GetAttached().GetSession().GetId(), initial.ID)
	}

	transport.dispatch(ptyInput("term-repl", "/status\n"))
	waitForPTYMessage(t, messages, 3*time.Second, func(message *ptypb.ProtocolMessage) bool {
		if value := message.GetError(); value != nil {
			t.Fatalf("unexpected pty error: %s", value.GetMessage())
		}
		return message.GetOutput() != nil && strings.Contains(string(message.GetOutput().GetData()), "not configured")
	})

	beforeExit, _ := mgr.Get(initial.ID)
	transport.dispatch(ptyInput("term-repl", "/exit\n"))
	waitForCondition(t, 3*time.Second, func() bool {
		info, ok := mgr.Get(initial.ID)
		return ok && info.State == proc.StateRunning && info.OutputBytes > beforeExit.OutputBytes
	})

	transport.dispatch(ptyInput("term-repl", "!tmux new-session -d -s webtask echo tmux_remote_ok\n"))
	waitForCondition(t, 3*time.Second, func() bool {
		for _, info := range mgr.List() {
			if info.Name == "webtask" {
				return true
			}
		}
		return false
	})

	// Ending one transport only releases its own monitor. A new transport
	// must reuse the same process-owned session and buffered console.
	transport.close()
	if info, ok := mgr.Get(initial.ID); !ok || info.State != proc.StateRunning {
		t.Fatalf("transport close terminated resident repl: %+v ok=%v", info, ok)
	}
	second := newPTYTransport(t, ctx, registry, 16)
	second.dispatch(&ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Attach{Attach: &ptypb.Attach{
		StreamId: "term-repl-2", SessionId: initial.ID,
	}}})
	attached := waitForPTYMessage(t, second.messages, time.Second, func(message *ptypb.ProtocolMessage) bool {
		return message.GetAttached() != nil
	})
	if attached.GetAttached().GetSession().GetId() != initial.ID {
		t.Fatalf("reconnect session = %s, want %s", attached.GetAttached().GetSession().GetId(), initial.ID)
	}

	running := 0
	for _, info := range mgr.List() {
		if info.State == proc.StateRunning && info.Kind == "repl" && info.Name == MainREPLName {
			running++
		}
	}
	if running != 1 {
		t.Fatalf("running main-repl count = %d, want 1", running)
	}

	// The console owns only its task. Closing it must leave Runtime and App usable.
	repl.Close()
	repl.Close()
	waitForCondition(t, time.Second, func() bool {
		info, ok := mgr.Get(initial.ID)
		return !ok || info.State != proc.StateRunning
	})
	session, err := rt.Runtime().OpenSession(ctx, agentsession.SessionOptions{ID: "after-console"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Command(ctx, "/status"); err != nil {
		t.Fatalf("console close broke the profile-owned runtime: %v", err)
	}
}

func TestEphemeralLocalREPLDoesNotCreateBufferedPTYConsole(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	application := newTestApp(t, telemetry.NopLogger(), apppkg.Dependencies{})

	applicationSet := loadConsoleApplication(t, ctx, application)
	defer applicationSet.Close(context.Background())
	rt, err := sessionext.New(agentsession.Config{Application: application, Option: &cfg.Option{REPLMode: "fast"}, Logger: telemetry.NopLogger(),
		PrimarySessionID: MainREPLName,
		Loop:             agent.StandardLoop{},
	})
	if err != nil {
		t.Fatalf("runtime without provider: %v", err)
	}

	rtSet := hosttest.Set(t, rt)
	if err := rtSet.Load(ctx); err != nil {
		t.Fatal(err)
	}
	defer rtSet.Close(context.Background())

	for _, info := range bashManager(rt.Runtime().App().Bash).List() {
		if info.Kind == "repl" && info.Name == MainREPLName {
			t.Fatalf("ephemeral local REPL was routed through buffered PTY: %+v", info)
		}
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func ptyInput(streamID, data string) *ptypb.ProtocolMessage {
	return &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Input{Input: &ptypb.Input{
		StreamId: streamID, Data: []byte(data),
	}}}
}

func waitForPTYMessage(t *testing.T, ch <-chan *ptypb.ProtocolMessage, timeout time.Duration, match func(*ptypb.ProtocolMessage) bool) *ptypb.ProtocolMessage {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case message := <-ch:
			if match(message) {
				return message
			}
		case <-deadline:
			t.Fatalf("timeout waiting for matching PTY message")
			return nil
		}
	}
}
