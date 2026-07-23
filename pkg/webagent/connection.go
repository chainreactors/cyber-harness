package webagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/ioa/protocols"
	"github.com/chainreactors/utils/pty"
	"github.com/gorilla/websocket"
)

// DefaultWSPath is the default WebSocket endpoint for agent connections.
const DefaultWSPath = "/api/agent/ws"

// connectionConfig holds all the parameters needed to establish and run a
// WebSocket connection to the hub.
type connectionConfig struct {
	ServerURL string
	WSPath    string
	Name      string
	// Token is an explicit bearer token; when empty the token embedded in
	// ServerURL's userinfo is used instead.
	Token    string
	Registry *commands.CommandRegistry
	AgentBus *eventbus.Bus[aop.Event]
	// DataBus and SCO enable tool.data / tool.sco event emission for tool-only
	// nodes; both are optional.
	DataBus *eventbus.Bus[output.ToolDataEvent]
	SCO     *output.SCOSidecar
	Logger  telemetry.Logger
	Chat    chatHandler
	Node    protocols.NodeRef
	Runtime webproto.AgentRuntime
	Status  func() webproto.AgentStatus
	Menu    func() []webproto.CommandSpec // nil = no command menu
	// RunnerFileRPC enables runner-only native directory operations. Regular
	// aiscan agents neither advertise nor accept these RPCs.
	RunnerFileRPC bool

	// PTYRouter creates a connection-scoped router. Agent transports receive it
	// from AgentRuntime; tool-only nodes fall back to their registry manager.
	PTYRouter func() (*pty.Router, error)
}

// chatHandler defines the Agent Runtime chat callbacks used by WebSocket transport.
// Implementations live in webagent or other packages that have access to the
// agent runtime and provider.
type chatHandler interface {
	// HandleChat runs a chat turn for one inbound AOP user message (event is
	// the decoded form of msg's payload). The node manages the cancellable
	// context and the chatCancels map. The EventRouter lets the handler
	// register agent session ID -> task ID mappings for event routing.
	// HandleChat admits the turn synchronously and returns the work that waits
	// for completion. This preserves WebSocket arrival order without blocking
	// the connection read loop for the duration of an agent run.
	HandleChat(ctx context.Context, msg webproto.Message, event aop.Event, send func(webproto.Message), router *eventRouter) func()

	// HandleUpload processes a file upload message.
	HandleUpload(msg webproto.Message, send func(webproto.Message))

	// HandleConfigReload processes a hub config push (LLM provider/model/key change).
	HandleConfigReload(serverURL string, send func(webproto.Message))

	// CancelChat attempts to cancel a running chat by task ID. Returns true if
	// the task was found and canceled.
	CancelChat(taskID string) bool
}

// eventRouter registers agent session -> task ID mappings so
// that agent events are routed to the correct WebSocket task.
type eventRouter struct {
	mu         *sync.Mutex
	eventRoute map[string]string // agent sessionID -> task messageID
}

// Route registers a mapping from an agent session ID to a WebSocket task ID.
func (r *eventRouter) Route(agentSessionID, taskID string) {
	r.mu.Lock()
	r.eventRoute[agentSessionID] = taskID
	r.mu.Unlock()
}

// Unroute removes all event route entries for the given task ID.
func (r *eventRouter) Unroute(taskID string) {
	r.mu.Lock()
	for sid, mid := range r.eventRoute {
		if mid == taskID {
			delete(r.eventRoute, sid)
		}
	}
	r.mu.Unlock()
}

// connect implements the reconnect loop. It calls connectOnce in a loop with
// agent.RetryDelay backoff. This is the main entry point for establishing a
// persistent WebSocket connection.
func connect(ctx context.Context, cc connectionConfig) error {
	if cc.WSPath == "" {
		cc.WSPath = DefaultWSPath
	}
	logger := cc.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}

	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := connectOnce(ctx, cc, logger)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			delay := agent.RetryDelay(attempt)
			attempt++
			logger.Warnf("connection lost (attempt %d), retrying in %v: %v", attempt, delay, err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(delay):
			}
		} else {
			attempt = 0
		}
	}
}

func connectOnce(ctx context.Context, cc connectionConfig, logger telemetry.Logger) error {
	if cc.Registry == nil {
		return fmt.Errorf("command registry is nil")
	}
	dialURL, accessKey := SplitAccessKey(cc.ServerURL)
	if cc.Token != "" {
		accessKey = cc.Token
	}
	wsURL := HTTPToWS(dialURL) + cc.WSPath
	var reqHeader http.Header
	if accessKey != "" {
		reqHeader = http.Header{"Authorization": {"Bearer " + accessKey}}
	}
	conn, wsResp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, reqHeader)
	if wsResp != nil && wsResp.Body != nil {
		wsResp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	defer conn.Close()

	sendCh := make(chan webproto.Message, 64)
	done := make(chan struct{})
	writeErr := make(chan error, 1)
	defer close(done)

	send := func(m webproto.Message) {
		select {
		case sendCh <- m:
		case <-done:
		}
	}

	stats := NewAgentStatsTracker()
	registration, err := RegisterPayload(cc.Name, cc.Registry, cc.Node, cc.Runtime, cc.Status, cc.Menu, stats.Snapshot())
	if err != nil {
		return err
	}
	regPayload, _ := json.Marshal(registration)
	if err := conn.WriteJSON(webproto.Message{Type: "register", Payload: regPayload}); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	var ack webproto.Message
	if err := conn.ReadJSON(&ack); err != nil || ack.Type != "connected" {
		return fmt.Errorf("expected connected ack")
	}

	// Writer goroutine: sendCh -> WebSocket.
	go func() {
		fail := func(err error) {
			select {
			case writeErr <- err:
			default:
			}
			// A failed writer must wake the reader so connectOnce returns and the
			// outer loop establishes a fresh connection.
			_ = conn.Close()
		}
		for {
			select {
			case msg, ok := <-sendCh:
				if !ok {
					return
				}
				if err := conn.WriteJSON(msg); err != nil {
					fail(err)
					return
				}
			case <-ctx.Done():
				if err := conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
					fail(err)
					return
				}
				_ = conn.Close()
				return
			case <-done:
				return
			}
		}
	}()

	if cc.Status != nil {
		go func(last webproto.AgentStatus) {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					next := cc.Status()
					if next != last {
						payload, _ := json.Marshal(next)
						send(webproto.Message{Type: "agent.status", Payload: payload})
						last = next
					}
				case <-ctx.Done():
					return
				case <-done:
					return
				}
			}
		}(registration.Status)
	}

	// Context close goroutine.
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	var mu sync.Mutex
	execTasks := make(map[string]context.CancelFunc)   // active tool.call tasks
	chatCancels := make(map[string]context.CancelFunc) // active chat messageID -> cancel
	eventRoute := make(map[string]string)              // agent SessionID -> messageID for event routing

	router := &eventRouter{mu: &mu, eventRoute: eventRoute}

	// Tool telemetry: scanner tool.data and normalized tool.sco events ride the
	// same connection, correlated to the calling task by call ID.
	if detach := attachToolEvents(cc.DataBus, cc.SCO, send); detach != nil {
		defer detach()
	}

	// Event bus subscription with stats tracking and event routing. Events
	// leave the agent kernel as AOP already; they are forwarded verbatim. The
	// only transformation is routing: a sub-session inherits its parent's task
	// route, learned from session.start's parent_session_id.
	if cc.AgentBus != nil {
		unsub := cc.AgentBus.Subscribe(func(e aop.Event) {
			if next, ok := stats.Observe(e); ok {
				statsPayload, _ := json.Marshal(next)
				send(webproto.Message{Type: "agent.stats", Payload: statsPayload})
			}
			mu.Lock()
			if e.Type == aop.TypeSessionStart {
				if data, err := aop.DecodeData[aop.SessionStartData](e); err == nil && data.ParentSessionID != "" {
					if parentTask := eventRoute[data.ParentSessionID]; parentTask != "" {
						eventRoute[e.SessionID] = parentTask
					}
				}
			}
			msgID := eventRoute[e.SessionID]
			mu.Unlock()
			// Events without a route belong to no hub task (resident REPL,
			// heartbeat); they reach the user through the PTY stream.
			if msgID == "" {
				return
			}
			payload, err := json.Marshal(e)
			if err != nil {
				return
			}
			send(webproto.Message{
				Type:    "aop",
				TaskID:  msgID,
				Payload: payload,
			})
		})
		defer unsub()
	}

	// PTY router setup.
	var ptyRouter *pty.Router
	if cc.PTYRouter != nil {
		ptyRouter, err = cc.PTYRouter()
	} else {
		ptyRouter = NewPTYRouter(cc.Registry)
	}
	if err != nil {
		return err
	}
	defer ptyRouter.Close()
	if cc.PTYRouter == nil {
		if mgr := RegistryPTYManager(cc.Registry); mgr != nil {
			unsub := SubscribePTYSessions(ctx, mgr, ptyRouter, send)
			defer unsub()
		}
	}

	// Main message dispatch loop.
	for {
		var msg webproto.Message
		if err := conn.ReadJSON(&msg); err != nil {
			select {
			case writerErr := <-writeErr:
				return fmt.Errorf("ws write: %w", writerErr)
			default:
			}
			return err
		}
		if ctx.Err() != nil {
			return nil
		}

		if msg.Type == webproto.TypePTY {
			frame, err := webproto.DecodePTYMessage(msg)
			if err != nil {
				send(webproto.NewPTYMessage(pty.Frame{Type: pty.FrameError, StreamID: frame.StreamID, Error: err.Error()}))
				continue
			}
			ptyRouter.Handle(ctx, frame, func(out pty.Frame) {
				send(webproto.NewPTYMessage(out))
			})
			continue
		}

		switch msg.Type {
		case "aop":
			// Inbound AOP carries two executable units on this transport: a
			// tool.call for the tool-only node surface, and a user message
			// (the chat surface). Everything else is ignored.
			var event aop.Event
			if json.Unmarshal(msg.Payload, &event) != nil {
				continue
			}
			inbound, err := agent.Classify(event)
			if err != nil {
				continue
			}
			if inbound.Kind == agent.InboundUserMessage {
				if cc.Chat == nil {
					continue
				}
				chatCtx, chatCancel := context.WithCancel(ctx)
				mu.Lock()
				chatCancels[msg.TaskID] = chatCancel
				mu.Unlock()
				run := cc.Chat.HandleChat(chatCtx, msg, event, send, router)
				go func(m webproto.Message, cCancel context.CancelFunc) {
					defer cCancel()
					defer func() {
						mu.Lock()
						delete(chatCancels, m.TaskID)
						// Clean up eventRoute entries for this task.
						for sid, mid := range eventRoute {
							if mid == m.TaskID {
								delete(eventRoute, sid)
							}
						}
						mu.Unlock()
					}()
					run()
				}(msg, chatCancel)
				continue
			}
			if inbound.Kind != agent.InboundToolCall {
				continue
			}
			taskCtx, cancel := context.WithCancel(ctx)
			mu.Lock()
			execTasks[msg.TaskID] = cancel
			mu.Unlock()
			router.Route(inbound.Event.SessionID, msg.TaskID)
			go func(m webproto.Message, inbound agent.Inbound, tCtx context.Context, tCancel context.CancelFunc) {
				defer tCancel()
				defer func() {
					mu.Lock()
					delete(execTasks, m.TaskID)
					mu.Unlock()
					router.Unroute(m.TaskID)
				}()
				handleAOPToolCall(tCtx, m, inbound, cc.Registry, cc.DataBus, send)
			}(msg, inbound, taskCtx, cancel)

		case "upload":
			if cc.Chat != nil {
				go cc.Chat.HandleUpload(msg, send)
			}

		case "file.read":
			go HandleFileRead(msg, cc.Runtime.WorkingDir, send)

		case "file.write":
			go HandleFileWrite(msg, cc.Runtime.WorkingDir, send)

		case "file.list":
			if cc.RunnerFileRPC {
				go HandleFileList(msg, cc.Runtime.WorkingDir, send)
			}

		case "file.mkdir":
			if cc.RunnerFileRPC {
				go HandleFileMkdir(msg, cc.Runtime.WorkingDir, send)
			}

		case "config":
			if cc.Chat != nil {
				go cc.Chat.HandleConfigReload(cc.ServerURL, send)
			}

		case "cancel":
			mu.Lock()
			if cancel, ok := execTasks[msg.TaskID]; ok {
				cancel()
			} else if cancel, ok := chatCancels[msg.TaskID]; ok {
				cancel()
			} else if cc.Chat != nil {
				cc.Chat.CancelChat(msg.TaskID)
			}
			mu.Unlock()
		}
	}
}
