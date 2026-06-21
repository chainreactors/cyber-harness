package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/gorilla/websocket"
)

// WSMessage is the single message type for all agent↔web communication.
type WSMessage = webproto.Message

// AgentInfo is the public view of a connected agent.
type AgentInfo struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Commands  []string               `json:"commands,omitempty"`
	Busy      bool                   `json:"busy"`
	ConnectAt time.Time              `json:"connected_at"`
	Identity  webproto.AgentIdentity `json:"identity,omitempty"`
	Stats     webproto.AgentStats    `json:"stats,omitempty"`
}

type taskResult struct {
	Output string
	Result json.RawMessage
	Err    string
}

type remoteAgent struct {
	id        string
	name      string
	commands  []string
	conn      *websocket.Conn
	sendCh    chan WSMessage
	connectAt time.Time
	identity  webproto.AgentIdentity
	stats     webproto.AgentStats

	mu    sync.Mutex
	tasks map[string]chan taskResult
	done  chan struct{}
}

func (a *remoteAgent) info() AgentInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AgentInfo{
		ID:        a.id,
		Name:      a.name,
		Commands:  a.commands,
		Busy:      len(a.tasks) > 0,
		ConnectAt: a.connectAt,
		Identity:  a.identity,
		Stats:     a.stats,
	}
}

// AgentPool manages connected remote aiscan agents via WebSocket.
type AgentPool struct {
	mu             sync.RWMutex
	agents         map[string]*remoteAgent
	hub            *Hub
	ptyMu          sync.RWMutex
	ptySubs        map[string]chan WSMessage
	ptyDrops       atomic.Int64
	allowedOrigins []string
}

func NewAgentPool(hub *Hub, allowedOrigins ...string) *AgentPool {
	return &AgentPool{
		agents:         make(map[string]*remoteAgent),
		hub:            hub,
		ptySubs:        make(map[string]chan WSMessage),
		allowedOrigins: allowedOrigins,
	}
}

func (p *AgentPool) register(a *remoteAgent) {
	p.mu.Lock()
	p.agents[a.id] = a
	p.mu.Unlock()
}

func (p *AgentPool) unregister(id string) {
	p.mu.Lock()
	a, ok := p.agents[id]
	delete(p.agents, id)
	p.mu.Unlock()
	if ok {
		a.mu.Lock()
		for _, ch := range a.tasks {
			close(ch)
		}
		a.tasks = nil
		a.mu.Unlock()
	}
}

func (p *AgentPool) get(id string) *remoteAgent {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.agents[id]
}

func (p *AgentPool) List() []AgentInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]AgentInfo, 0, len(p.agents))
	for _, a := range p.agents {
		out = append(out, a.info())
	}
	return out
}

func (p *AgentPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.agents)
}

// Pick selects an idle agent, or any agent if none idle.
func (p *AgentPool) Pick() *remoteAgent {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var fallback *remoteAgent
	for _, a := range p.agents {
		a.mu.Lock()
		busy := len(a.tasks) > 0
		a.mu.Unlock()
		if !busy {
			return a
		}
		if fallback == nil {
			fallback = a
		}
	}
	return fallback
}

// DispatchCommand sends a command to an agent and returns a channel for the result.
func (p *AgentPool) DispatchCommand(agentID, taskID, command string) (<-chan taskResult, error) {
	a := p.get(agentID)
	if a == nil {
		return nil, fmt.Errorf("agent %s not connected", agentID)
	}
	ch := make(chan taskResult, 1)
	a.mu.Lock()
	a.tasks[taskID] = ch
	a.mu.Unlock()

	select {
	case a.sendCh <- WSMessage{Type: "exec", TaskID: taskID, Data: command}:
	default:
		a.mu.Lock()
		delete(a.tasks, taskID)
		a.mu.Unlock()
		close(ch)
		return nil, fmt.Errorf("agent %s send channel full", agentID)
	}
	return ch, nil
}

func (p *AgentPool) SendAgentMessage(agentID string, msg WSMessage) error {
	a := p.get(agentID)
	if a == nil {
		return fmt.Errorf("agent %s not connected", agentID)
	}
	select {
	case a.sendCh <- msg:
		return nil
	default:
		return fmt.Errorf("agent %s send channel full", agentID)
	}
}

func (p *AgentPool) CancelTask(agentID, taskID string) {
	a := p.get(agentID)
	if a == nil {
		return
	}
	select {
	case a.sendCh <- WSMessage{Type: "cancel", TaskID: taskID}:
	default:
	}
}

// HandleTerminalWS bridges one browser terminal WebSocket to one remote agent.
// The browser sends pty.* messages; the pool assigns a stream_id and relays
// matching agent responses back.
func (p *AgentPool) HandleTerminalWS(agentID string, w http.ResponseWriter, r *http.Request) {
	if p.get(agentID) == nil {
		writeError(w, http.StatusNotFound, "agent not connected")
		return
	}

	conn, err := p.upgrader().Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	terminalID := generateID()
	events, unsubscribe := p.subscribePTY(terminalID)
	defer unsubscribe()
	defer p.CloseTerminal(agentID, terminalID)

	done := make(chan struct{})
	defer close(done)

	var writeMu sync.Mutex
	write := func(msg WSMessage) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(msg)
	}

	go func() {
		for {
			select {
			case msg, ok := <-events:
				if !ok {
					return
				}
				_ = write(msg)
			case <-done:
				return
			}
		}
	}()

	for {
		var msg WSMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		if !isTerminalMessage(msg.Type) {
			_ = write(WSMessage{Type: "pty.error", StreamID: terminalID, Data: "unsupported terminal message"})
			continue
		}
		msg.StreamID = terminalID
		msg.TaskID = ""
		if err := p.SendAgentMessage(agentID, msg); err != nil {
			_ = write(WSMessage{Type: "pty.error", StreamID: terminalID, Data: err.Error()})
			return
		}
	}
}

func (p *AgentPool) CancelPTY(agentID, terminalID string) {
	_ = p.SendAgentMessage(agentID, WSMessage{Type: "pty.kill", StreamID: terminalID})
}

func (p *AgentPool) CloseTerminal(agentID, terminalID string) {
	_ = p.SendAgentMessage(agentID, WSMessage{Type: "pty.detach", StreamID: terminalID})
}

func isTerminalMessage(msgType string) bool {
	return strings.HasPrefix(msgType, "pty.")
}

func (p *AgentPool) subscribePTY(terminalID string) (<-chan WSMessage, func()) {
	ch := make(chan WSMessage, 256)
	p.ptyMu.Lock()
	p.ptySubs[terminalID] = ch
	p.ptyMu.Unlock()
	return ch, func() {
		p.ptyMu.Lock()
		if p.ptySubs[terminalID] == ch {
			delete(p.ptySubs, terminalID)
			close(ch)
		}
		p.ptyMu.Unlock()
	}
}

func (p *AgentPool) forwardPTYMessage(msg WSMessage) bool {
	if !isTerminalMessage(msg.Type) || msg.StreamID == "" {
		return false
	}
	p.ptyMu.RLock()
	ch := p.ptySubs[msg.StreamID]
	if ch != nil {
		select {
		case ch <- msg:
		default:
			p.ptyDrops.Add(1)
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- msg:
			default:
				p.ptyDrops.Add(1)
			}
		}
	}
	p.ptyMu.RUnlock()
	return ch != nil
}

// --- WebSocket handler ---

func (p *AgentPool) upgrader() *websocket.Upgrader {
	if len(p.allowedOrigins) == 0 {
		return &websocket.Upgrader{}
	}
	origins := p.allowedOrigins
	return &websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			for _, o := range origins {
				if o == "*" || o == origin {
					return true
				}
			}
			return false
		},
	}
}

// HandleWS upgrades to WebSocket and manages the agent lifecycle.
// This single endpoint replaces register + stream + output + complete.
func (p *AgentPool) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := p.upgrader().Upgrade(w, r, nil)
	if err != nil {
		return
	}

	// First message must be register.
	var reg WSMessage
	if err := conn.ReadJSON(&reg); err != nil || reg.Type != "register" {
		conn.Close()
		return
	}
	var info webproto.RegisterPayload
	if reg.Payload != nil {
		_ = json.Unmarshal(reg.Payload, &info)
	}
	if info.Name == "" {
		info.Name = "agent"
	}

	agent := &remoteAgent{
		id:        generateID(),
		name:      info.Name,
		commands:  info.Commands,
		conn:      conn,
		sendCh:    make(chan WSMessage, 32),
		connectAt: time.Now(),
		identity:  info.Identity,
		stats:     info.Stats,
		tasks:     make(map[string]chan taskResult),
		done:      make(chan struct{}),
	}
	p.register(agent)
	defer func() {
		p.unregister(agent.id)
		conn.Close()
		close(agent.done)
	}()

	// Send connected ack.
	ack, _ := json.Marshal(map[string]string{"agent_id": agent.id, "name": agent.name})
	_ = conn.WriteJSON(WSMessage{Type: "connected", Payload: ack})

	// Write goroutine: sendCh → WebSocket.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case msg, ok := <-agent.sendCh:
				if !ok {
					return
				}
				if err := conn.WriteJSON(msg); err != nil {
					return
				}
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			case <-agent.done:
				return
			}
		}
	}()

	// Read loop: WebSocket → dispatch.
	for {
		var msg WSMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		p.handleAgentMessage(agent, msg)
	}
}

func (p *AgentPool) handleAgentMessage(a *remoteAgent, msg WSMessage) {
	if p.forwardPTYMessage(msg) {
		return
	}

	switch msg.Type {
	case "agent.stats":
		var stats webproto.AgentStats
		if len(msg.Payload) > 0 && json.Unmarshal(msg.Payload, &stats) == nil {
			a.mu.Lock()
			a.stats = stats
			a.mu.Unlock()
		}

	case "output":
		if p.hub != nil && msg.TaskID != "" {
			data := stripANSI(msg.Data)
			if data == "" {
				return
			}
			p.hub.Broadcast(msg.TaskID, HubEvent{
				Type: "progress",
				Data: mustJSON(map[string]string{"scan_id": msg.TaskID, "data": data}),
			})
		}

	case "complete":
		a.mu.Lock()
		ch, ok := a.tasks[msg.TaskID]
		if ok {
			delete(a.tasks, msg.TaskID)
		}
		a.mu.Unlock()
		if ok && ch != nil {
			res := taskResult{Output: msg.Data, Result: msg.Payload}
			ch <- res
			close(ch)
		}
		p.recordScanResultStats(a, msg.Payload)

	case "error":
		a.mu.Lock()
		ch, ok := a.tasks[msg.TaskID]
		if ok {
			delete(a.tasks, msg.TaskID)
		}
		a.mu.Unlock()
		if ok && ch != nil {
			ch <- taskResult{Err: msg.Data}
			close(ch)
		}

	default:
		// Agent events (agent.*, log.*, scanner.*) are shown in the same
		// progress stream as scanner output for the task that produced them.
		if p.hub != nil && msg.TaskID != "" {
			raw, _ := json.Marshal(map[string]string{
				"scan_id": msg.TaskID,
				"data":    formatTelemetryProgress(msg),
			})
			p.hub.Broadcast(msg.TaskID, HubEvent{Type: "progress", Data: raw})
		}
	}
}

func (p *AgentPool) recordScanResultStats(a *remoteAgent, payload json.RawMessage) {
	if a == nil || len(payload) == 0 {
		return
	}
	var result output.Result
	if err := json.Unmarshal(payload, &result); err != nil {
		return
	}
	a.mu.Lock()
	a.stats.Assets += len(result.Assets)
	if result.Summary.Loots > 0 {
		a.stats.Loots += result.Summary.Loots
	} else {
		a.stats.Loots += len(result.Loots)
	}
	a.mu.Unlock()
}

func formatTelemetryProgress(msg WSMessage) string {
	if msg.Data == "" {
		return "[" + msg.Type + "]"
	}
	return fmt.Sprintf("[%s] %s", msg.Type, msg.Data)
}
