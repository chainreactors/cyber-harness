package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/utils/pty"
	"github.com/gorilla/websocket"
)

// DefaultWSPath is the default WebSocket endpoint for agent connections.
const DefaultWSPath = "/api/agent/ws"

// ConnectConfig holds all the parameters needed to establish and run a
// WebSocket connection to the hub.
type ConnectConfig struct {
	ServerURL string
	WSPath    string
	Name      string
	Registry  *commands.CommandRegistry
	AgentBus  *eventbus.Bus[agent.Event]
	DataBus   *eventbus.Bus[output.ToolDataEvent]
	SCO       *output.SCOSidecar
	Logger    telemetry.Logger
	Chat      ChatHandler                    // nil for runner (no LLM)
	Identity  func() webproto.AgentIdentity  // nil = use default OS identity
	Menu      func() []webproto.CommandSpec   // nil = no command menu

	// ExtraPTYOpeners provides additional PTY openers (e.g. a REPL opener from
	// the agent runtime) without requiring a core/runner import.
	ExtraPTYOpeners map[string]pty.OpenFunc
}

// ChatHandler defines the interface for handling LLM chat interactions.
// Implementations live in webagent or other packages that have access to the
// agent runtime and provider.
type ChatHandler interface {
	// HandleChat runs a chat turn. The node manages the cancellable context and
	// the chatCancels map. The EventRouter lets the handler register agent
	// session ID -> task ID mappings for event routing.
	HandleChat(ctx context.Context, msg webproto.Message, send func(webproto.Message), router *EventRouter)

	// HandleUpload processes a file upload message.
	HandleUpload(msg webproto.Message, send func(webproto.Message))

	// HandleConfigReload processes a hub config push (LLM provider/model/key change).
	HandleConfigReload(serverURL string, send func(webproto.Message))

	// CancelChat attempts to cancel a running chat by task ID. Returns true if
	// the task was found and canceled.
	CancelChat(taskID string) bool
}

// EventRouter lets ChatHandler register agent session -> task ID mappings so
// that agent events are routed to the correct WebSocket task.
type EventRouter struct {
	mu         *sync.Mutex
	eventRoute map[string]string // agent sessionID -> task messageID
}

// Route registers a mapping from an agent session ID to a WebSocket task ID.
func (r *EventRouter) Route(agentSessionID, taskID string) {
	r.mu.Lock()
	r.eventRoute[agentSessionID] = taskID
	r.mu.Unlock()
}

// Unroute removes all event route entries for the given task ID.
func (r *EventRouter) Unroute(taskID string) {
	r.mu.Lock()
	for sid, mid := range r.eventRoute {
		if mid == taskID {
			delete(r.eventRoute, sid)
		}
	}
	r.mu.Unlock()
}

// Connect implements the reconnect loop. It calls connectOnce in a loop with
// agent.RetryDelay backoff. This is the main entry point for establishing a
// persistent WebSocket connection.
func Connect(ctx context.Context, cc ConnectConfig) error {
	if cc.WSPath == "" {
		cc.WSPath = DefaultWSPath
	}
	logger := cc.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}

	pipeline := BuildDataPipeline(cc.DataBus, cc.SCO)
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := connectOnce(ctx, cc, pipeline, logger)
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

func connectOnce(ctx context.Context, cc ConnectConfig, pipeline *DataPipeline, logger telemetry.Logger) error {
	if cc.Registry == nil {
		return fmt.Errorf("command registry is nil")
	}
	dialURL, accessKey := SplitAccessKey(cc.ServerURL)
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
	defer close(done)

	send := func(m webproto.Message) {
		select {
		case sendCh <- m:
		case <-done:
		}
	}

	stats := NewAgentStatsTracker()
	regPayload, _ := json.Marshal(RegisterPayload(cc.Name, cc.Registry, cc.Identity, cc.Menu, stats.Snapshot()))
	if err := conn.WriteJSON(webproto.Message{Type: "register", Payload: regPayload}); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	var ack webproto.Message
	if err := conn.ReadJSON(&ack); err != nil || ack.Type != "connected" {
		return fmt.Errorf("expected connected ack")
	}

	if pipeline != nil {
		pipeline.Attach(send)
		defer pipeline.Detach()
	}

	// Writer goroutine: sendCh -> WebSocket.
	go func() {
		for {
			select {
			case msg, ok := <-sendCh:
				if !ok {
					return
				}
				_ = conn.WriteJSON(msg)
			case <-ctx.Done():
				_ = conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			case <-done:
				return
			}
		}
	}()

	// Context close goroutine.
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	var mu sync.Mutex
	execTasks := make(map[string]context.CancelFunc)   // tmux-managed exec tasks
	chatCancels := make(map[string]context.CancelFunc) // active chat messageID -> cancel
	eventRoute := make(map[string]string)              // agent SessionID -> messageID for event routing

	router := &EventRouter{mu: &mu, eventRoute: eventRoute}

	// Event bus subscription with stats tracking and event routing.
	if cc.AgentBus != nil {
		unsub := cc.AgentBus.Subscribe(func(e agent.Event) {
			if next, ok := stats.Observe(e); ok {
				statsPayload, _ := json.Marshal(next)
				send(webproto.Message{Type: "agent.stats", Payload: statsPayload})
			}
			aopEvents := aop.FromAgentEvent(e, "aiscan")
			data := AgentEventSummary(e)
			mu.Lock()
			msgID := eventRoute[e.SessionID]
			if msgID == "" && e.ParentSessionID != "" {
				msgID = eventRoute[e.ParentSessionID]
				if msgID != "" {
					eventRoute[e.SessionID] = msgID
				}
			}
			var targets []string
			if msgID != "" {
				targets = []string{msgID}
			} else {
				for tid := range execTasks {
					targets = append(targets, tid)
				}
			}
			mu.Unlock()
			for _, aopEv := range aopEvents {
				payload, _ := json.Marshal(aopEv)
				summary := data
				if summary == "" {
					summary = string(payload)
				}
				for _, id := range targets {
					send(webproto.Message{
						Type:    "aop." + aopEv.Type,
						TaskID:  id,
						Data:    summary,
						Payload: payload,
					})
				}
			}
		})
		defer unsub()
	}

	// PTY router setup.
	ptyRouter := NewPTYRouter(cc.Registry, cc.ExtraPTYOpeners)
	defer ptyRouter.Close()
	if mgr := RegistryPTYManager(cc.Registry); mgr != nil {
		unsub := SubscribePTYSessions(ctx, mgr, ptyRouter, send)
		defer unsub()
	}

	// Main message dispatch loop.
	for {
		var msg webproto.Message
		if err := conn.ReadJSON(&msg); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}

		if strings.HasPrefix(msg.Type, "pty.") {
			frame, err := webproto.MessageToFrame(msg)
			if err != nil {
				send(webproto.Message{Type: "pty.error", StreamID: msg.StreamID, Data: err.Error()})
				continue
			}
			ptyRouter.Handle(ctx, frame, func(out pty.Frame) {
				send(webproto.FrameToMessage(out))
			})
			continue
		}

		switch msg.Type {
		case "exec":
			taskCtx, cancel := context.WithCancel(ctx)
			mu.Lock()
			execTasks[msg.TaskID] = cancel
			mu.Unlock()
			go func(m webproto.Message, tCtx context.Context, tCancel context.CancelFunc) {
				defer tCancel()
				defer func() {
					mu.Lock()
					delete(execTasks, m.TaskID)
					mu.Unlock()
				}()
				ExecCommand(tCtx, m, cc.Registry, send)
			}(msg, taskCtx, cancel)

		case "chat":
			if cc.Chat != nil {
				chatCtx, chatCancel := context.WithCancel(ctx)
				mu.Lock()
				chatCancels[msg.TaskID] = chatCancel
				mu.Unlock()
				go func(m webproto.Message, cCtx context.Context, cCancel context.CancelFunc) {
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
					cc.Chat.HandleChat(cCtx, m, send, router)
				}(msg, chatCtx, chatCancel)
			}

		case "upload":
			if cc.Chat != nil {
				go cc.Chat.HandleUpload(msg, send)
			}

		case "file.read":
			go HandleFileRead(msg, send)

		case "file.write":
			go HandleFileWrite(msg, send)

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
