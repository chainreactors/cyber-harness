package node

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

	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/namespaces"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	"github.com/chainreactors/cyber/pkg/harness"
	"github.com/chainreactors/cyber/pkg/profile"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

type reloadTestProfile struct {
	*harness.Harness
	fail       bool
	closed     atomic.Bool
	protocols  *extension.Set
	namespaces *namespaces.Registry
}

func (p *reloadTestProfile) Load(ctx context.Context) error {
	if p.fail {
		return errors.New("candidate load failed")
	}
	if err := p.Harness.Load(ctx); err != nil {
		return err
	}
	rt, err := p.Runtime()
	if err != nil {
		return err
	}
	p.namespaces = namespaces.New()
	p.protocols, err = extension.New(p.namespaces, extension.Provided(rt), sessionext.NewProtocol())
	if err != nil {
		return err
	}
	return p.protocols.Load(ctx)
}
func (p *reloadTestProfile) Close(ctx context.Context) error {
	var err error
	if p.protocols != nil {
		err = p.protocols.Close(ctx)
	}
	err = errors.Join(err, p.Harness.Close(ctx))
	p.closed.Store(true)
	return err
}
func (p *reloadTestProfile) RegisterNamespaces(mux *aop.NamespaceMux) error {
	return p.namespaces.Bind(mux)
}
func (*reloadTestProfile) AgentStatus() *aop.AgentStatus         { return &aop.AgentStatus{} }
func (*reloadTestProfile) ConsoleBindings() *consoleapi.Bindings { return nil }

type reloadWaitTool struct {
	started chan struct{}
	drained atomic.Bool
}

func (*reloadWaitTool) Name() string        { return "reload_wait" }
func (*reloadWaitTool) Description() string { return "wait for profile cancellation" }
func (w *reloadWaitTool) Definition() *aop.ToolDefinition {
	return coretool.Def(w.Name(), w.Description(), struct{}{})
}
func (w *reloadWaitTool) Execute(ctx context.Context, _ string) (*coretool.Result, error) {
	close(w.started)
	<-ctx.Done()
	w.drained.Store(true)
	return nil, ctx.Err()
}

func TestRemoteReloadKeepsFailedProfileAndDrainsSuccessfulSwitch(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	work := &reloadWaitTool{started: make(chan struct{})}
	directory := t.TempDir()
	var mu sync.Mutex
	var built []*reloadTestProfile
	build := func(request profile.Request) (profile.Profile, error) {
		if request.ProviderMode != profile.ProviderDisabled {
			return nil, fmt.Errorf("empty server LLM must disable the node provider")
		}
		h, err := harness.New(harness.Config{Base: harness.BaseConfig{Directory: directory, Provider: provider.StartupConfig{Mode: provider.StartupDisabled}}, Session: request.Session,
			Extensions: []extension.Extension{extension.Func{LoadFunc: func(scope *extension.Scope) error { return extension.Add[coretool.Tool](scope, work) }}},
		})
		if err != nil {
			return nil, err
		}
		p := &reloadTestProfile{Harness: h, fail: request.Option.Heartbeat < 0}
		mu.Lock()
		built = append(built, p)
		mu.Unlock()
		return p, nil
	}
	var connections atomic.Int32
	serverDone := make(chan error, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(12 * time.Second))
		send := func(id, reply string, message proto.Message) error {
			data, err := proto.Marshal(aop.MustWrap(id, reply, message))
			if err != nil {
				return err
			}
			return conn.WriteMessage(websocket.BinaryMessage, data)
		}
		receive := func() (*aop.Envelope, error) {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return nil, err
			}
			value := new(aop.Envelope)
			err = proto.Unmarshal(data, value)
			return value, err
		}
		waitReload := func(id string) (*types.ReloadResult, error) {
			for {
				envelope, err := receive()
				if err != nil {
					return nil, err
				}
				if envelope.ReplyTo != id {
					continue
				}
				message, err := aop.Unwrap(envelope)
				if err != nil {
					return nil, err
				}
				if value, ok := message.(*types.ReloadProtocolMessage); ok {
					return value.GetResult(), nil
				}
			}
		}
		reload := func(id string, heartbeat int32) (*types.ReloadResult, error) {
			err := send(id, "", &types.ReloadProtocolMessage{Message: &types.ReloadProtocolMessage_Request{Request: &types.ReloadRequest{Config: &types.DistributeConfig{Agent: &types.AgentConfig{Heartbeat: heartbeat}}}}})
			if err != nil {
				return nil, err
			}
			return waitReload(id)
		}
		failure := func(err error) { serverDone <- err }
		hello, err := receive()
		if err != nil {
			failure(err)
			return
		}
		if err := send("accepted", hello.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "reload-test"}}}); err != nil {
			failure(err)
			return
		}
		if connections.Add(1) == 1 {
			result, err := reload("bad", -1)
			if err != nil || result.GetOk() {
				failure(fmt.Errorf("failed candidate: %v %v", result, err))
				return
			}
			mu.Lock()
			old, failed := built[0], built[1]
			mu.Unlock()
			if old.closed.Load() || !failed.closed.Load() {
				failure(errors.New("failure closed current or leaked candidate"))
				return
			}
			args, _ := aop.JSONValue(map[string]any{})
			if err := send("work", "", &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{Call: &toolpb.Call{Call: &aop.ToolCall{Id: "work", Name: work.Name(), Arguments: args}}}}); err != nil {
				failure(err)
				return
			}
			select {
			case <-work.started:
			case <-ctx.Done():
				failure(ctx.Err())
				return
			}
			result, err = reload("good", 1)
			if err != nil || !result.GetOk() {
				failure(fmt.Errorf("successful candidate: %v %v", result, err))
				return
			}
			// The node closes this connection after replying to the reload.
			for {
				if _, err := receive(); err != nil {
					return
				}
			}
		}
		result, err := reload("same", 1)
		if err != nil || !result.GetOk() {
			failure(fmt.Errorf("same config: %v %v", result, err))
			return
		}
		mu.Lock()
		count, old := len(built), built[0]
		mu.Unlock()
		if count != 3 || !old.closed.Load() || !work.drained.Load() {
			failure(fmt.Errorf("builds=%d oldClosed=%v workDrained=%v", count, old.closed.Load(), work.drained.Load()))
			return
		}
		serverDone <- nil
		<-ctx.Done()
	}))
	defer server.Close()
	nodeDone := make(chan error, 1)
	go func() {
		nodeDone <- RunWebSocket(ctx, build, &cfg.Option{Explicit: map[string]bool{}, NodeOptions: cfg.NodeOptions{NodeID: "reload-test"}, AgentOptions: cfg.AgentOptions{ServerURL: server.URL}}, telemetry.NopLogger())
	}()
	select {
	case err := <-serverDone:
		if err != nil {
			cancel()
			t.Fatal(err)
		}
	case err := <-nodeDone:
		cancel()
		t.Fatalf("node exited early: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	select {
	case <-nodeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("node did not drain")
	}
}
