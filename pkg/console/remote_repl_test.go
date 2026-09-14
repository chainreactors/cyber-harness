package console

import (
	"context"
	"fmt"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/internal/extensiontest"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent"
	ptypb "github.com/chainreactors/aiscan/aop/pty"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/commands"
	sessionext "github.com/chainreactors/aiscan/pkg/exts/session"
	"github.com/chainreactors/aiscan/pkg/terminal"
	"github.com/chainreactors/utils/pty"
)

func newPTYRouter(bash *commands.BashTool) (*terminal.Router, error) {
	manager := bashManager(bash)
	if manager == nil || manager.Manager == nil {
		return nil, fmt.Errorf("pty manager unavailable")
	}
	return terminal.NewRuntimeRouter(manager.Manager), nil
}

func TestConsoleOwnsPersistentMainREPLWithoutProvider(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	option := &cfg.Option{REPLMode: "fast"}
	application := apppkg.New(apppkg.Config{SkipEngines: true, Logger: telemetry.NopLogger()}, apppkg.Dependencies{})

	applicationSet := loadConsoleApplication(t, ctx, application)
	defer applicationSet.Close(context.Background())
	rt, err := sessionext.New(application.App, nil, option, telemetry.NopLogger(), sessionext.Config{
		PrimarySessionID: MainREPLName,
		Loop:             agent.StandardLoop{},
	})
	if err != nil {
		t.Fatalf("runtime without provider: %v", err)
	}

	rtSet := extensiontest.Set(t, extension.Entry{ID: "rt", Extension: rt})
	if err := rtSet.Load(ctx); err != nil {
		t.Fatal(err)
	}
	defer rtSet.Close(context.Background())

	repl, err := StartPersistent(rt.Manager, option)
	if err != nil {
		t.Fatal(err)
	}
	defer repl.Close()
	mgr := bashManager(rt.Manager.App().Bash)
	if mgr == nil {
		t.Fatal("pty manager unavailable")
	}

	var initial pty.Info
	for _, info := range mgr.List() {
		if info.State == pty.StateRunning && info.Kind == "repl" && info.Name == MainREPLName {
			initial = info
			break
		}
	}
	if initial.ID == "" {
		t.Fatal("main-repl was not created eagerly")
	}
	if initial.Name != MainREPLName || initial.Kind != "repl" || initial.State != pty.StateRunning {
		t.Fatalf("unexpected resident repl: %+v", initial)
	}

	messages := make(chan *ptypb.ProtocolMessage, 64)
	router, err := newPTYRouter(rt.Manager.App().Bash)
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()

	router.Handle(ctx, &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Attach{Attach: &ptypb.Attach{
		StreamId: "term-repl", SessionId: initial.ID,
	}}}, func(message *ptypb.ProtocolMessage) { messages <- message })
	opened := waitForPTYMessage(t, messages, time.Second, func(message *ptypb.ProtocolMessage) bool {
		if value := message.GetError(); value != nil {
			t.Fatalf("unexpected pty error: %s", value.GetMessage())
		}
		return message.GetAttached() != nil
	})
	if opened.GetAttached().GetSession().GetId() != initial.ID {
		t.Fatalf("transport created a second repl: got %s want %s", opened.GetAttached().GetSession().GetId(), initial.ID)
	}

	router.Handle(ctx, ptyInput("term-repl", "/status\n"), func(message *ptypb.ProtocolMessage) {
		messages <- message
	})
	waitForPTYMessage(t, messages, 3*time.Second, func(message *ptypb.ProtocolMessage) bool {
		if value := message.GetError(); value != nil {
			t.Fatalf("unexpected pty error: %s", value.GetMessage())
		}
		return message.GetOutput() != nil && strings.Contains(string(message.GetOutput().GetData()), "not configured")
	})

	beforeExit, _ := mgr.Get(initial.ID)
	router.Handle(ctx, ptyInput("term-repl", "/exit\n"), func(message *ptypb.ProtocolMessage) {
		messages <- message
	})
	waitForCondition(t, 3*time.Second, func() bool {
		info, ok := mgr.Get(initial.ID)
		return ok && info.State == pty.StateRunning && info.OutputBytes > beforeExit.OutputBytes
	})

	router.Handle(ctx, ptyInput("term-repl", "!tmux new-session -d -s webtask echo tmux_remote_ok\n"), func(message *ptypb.ProtocolMessage) {
		messages <- message
	})
	waitForCondition(t, 3*time.Second, func() bool {
		for _, info := range mgr.List() {
			if info.Name == "webtask" {
				return true
			}
		}
		return false
	})

	// Closing one transport Router only detaches its monitor. A new transport
	// must reuse the same process-owned session and buffered console.
	router.Close()
	if info, ok := mgr.Get(initial.ID); !ok || info.State != pty.StateRunning {
		t.Fatalf("router close terminated resident repl: %+v ok=%v", info, ok)
	}
	router2, err := newPTYRouter(rt.Manager.App().Bash)
	if err != nil {
		t.Fatal(err)
	}
	defer router2.Close()
	reconnected := make(chan *ptypb.ProtocolMessage, 16)
	router2.Handle(ctx, &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Attach{Attach: &ptypb.Attach{
		StreamId: "term-repl-2", SessionId: initial.ID,
	}}}, func(message *ptypb.ProtocolMessage) {
		reconnected <- message
	})
	attached := waitForPTYMessage(t, reconnected, time.Second, func(message *ptypb.ProtocolMessage) bool {
		return message.GetAttached() != nil
	})
	if attached.GetAttached().GetSession().GetId() != initial.ID {
		t.Fatalf("reconnect session = %s, want %s", attached.GetAttached().GetSession().GetId(), initial.ID)
	}

	running := 0
	for _, info := range mgr.List() {
		if info.State == pty.StateRunning && info.Kind == "repl" && info.Name == MainREPLName {
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
		return !ok || info.State != pty.StateRunning
	})
	session, err := rt.Manager.OpenSession(ctx, sessionext.SessionOptions{ID: "after-console"})
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

	application := apppkg.New(apppkg.Config{SkipEngines: true, Logger: telemetry.NopLogger()}, apppkg.Dependencies{})

	applicationSet := loadConsoleApplication(t, ctx, application)
	defer applicationSet.Close(context.Background())
	rt, err := sessionext.New(application.App, nil, &cfg.Option{REPLMode: "fast"}, telemetry.NopLogger(), sessionext.Config{
		PrimarySessionID: MainREPLName,
		Loop:             agent.StandardLoop{},
	})
	if err != nil {
		t.Fatalf("runtime without provider: %v", err)
	}

	rtSet := extensiontest.Set(t, extension.Entry{ID: "rt", Extension: rt})
	if err := rtSet.Load(ctx); err != nil {
		t.Fatal(err)
	}
	defer rtSet.Close(context.Background())

	for _, info := range bashManager(rt.Manager.App().Bash).List() {
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
