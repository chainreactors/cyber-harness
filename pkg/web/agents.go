package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/runner"
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
	Turn   int
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
	turns map[string]int
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

// SessionLookup resolves a task ID to its owning chat session.
type SessionLookup interface {
	TaskSession(taskID string) (sessionID string, ok bool)
	BroadcastChatEvent(sessionID string, event ChatEvent)
}

// RecordStore is the subset of Store needed for record persistence.
type RecordStore interface {
	InsertRecord(ctx context.Context, rec *output.Record) error
	InsertRecords(ctx context.Context, recs []*output.Record) error
}

// AgentPool manages connected remote aiscan agents via WebSocket.
type AgentPool struct {
	mu             sync.RWMutex
	agents         map[string]*remoteAgent
	hub            *Hub
	sessions       SessionLookup
	records        RecordStore
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

func (p *AgentPool) SetSessionLookup(sl SessionLookup) {
	p.sessions = sl
}

func (p *AgentPool) SetRecordStore(rs RecordStore) {
	p.records = rs
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

// PickChat selects an idle LLM-capable agent, or any LLM-capable agent if all
// are busy.
func (p *AgentPool) PickChat() *remoteAgent {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var fallback *remoteAgent
	for _, a := range p.agents {
		a.mu.Lock()
		busy := len(a.tasks) > 0
		chatCapable := a.identity.Provider != ""
		a.mu.Unlock()
		if !chatCapable {
			continue
		}
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
	return p.dispatch(agentID, taskID, "exec", command)
}

// DispatchChat sends a natural-language prompt to an LLM-capable agent.
func (p *AgentPool) DispatchChat(agentID, taskID, prompt string) (<-chan taskResult, error) {
	return p.DispatchChatSession(agentID, taskID, "", prompt)
}

// DispatchChatSession sends chat input to an agent and scopes the remote
// agent-side conversation state to the web chat session.
func (p *AgentPool) DispatchChatSession(agentID, taskID, sessionID, prompt string) (<-chan taskResult, error) {
	var payload json.RawMessage
	if sessionID != "" {
		payload = mustJSON(map[string]string{"session_id": sessionID})
	}
	return p.dispatchPayload(agentID, taskID, "chat", prompt, payload)
}

func (p *AgentPool) dispatch(agentID, taskID, typ, data string) (<-chan taskResult, error) {
	return p.dispatchPayload(agentID, taskID, typ, data, nil)
}

func (p *AgentPool) dispatchPayload(agentID, taskID, typ, data string, payload json.RawMessage) (<-chan taskResult, error) {
	a := p.get(agentID)
	if a == nil {
		return nil, fmt.Errorf("agent %s not connected", agentID)
	}
	ch := make(chan taskResult, 1)
	a.mu.Lock()
	a.tasks[taskID] = ch
	a.turns[taskID] = 0
	a.mu.Unlock()

	select {
	case a.sendCh <- WSMessage{Type: typ, TaskID: taskID, Data: data, Payload: payload}:
	default:
		a.mu.Lock()
		delete(a.tasks, taskID)
		delete(a.turns, taskID)
		a.mu.Unlock()
		close(ch)
		return nil, fmt.Errorf("agent %s send channel full", agentID)
	}
	return ch, nil
}

func (p *AgentPool) dispatchMessage(agentID, taskID string, msg WSMessage) (<-chan taskResult, error) {
	a := p.get(agentID)
	if a == nil {
		return nil, fmt.Errorf("agent %s not connected", agentID)
	}
	ch := make(chan taskResult, 1)
	a.mu.Lock()
	a.tasks[taskID] = ch
	a.turns[taskID] = 0
	a.mu.Unlock()

	select {
	case a.sendCh <- msg:
	default:
		a.mu.Lock()
		delete(a.tasks, taskID)
		delete(a.turns, taskID)
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
		turns:     make(map[string]int),
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
			p.forwardToSession(a, msg.TaskID, ChatEvent{
				Type:   ChatEventScanProgress,
				ScanID: msg.TaskID,
				Data:   data,
			})
		}

	case "complete":
		a.mu.Lock()
		ch, ok := a.tasks[msg.TaskID]
		turn := a.turns[msg.TaskID]
		if ok {
			delete(a.tasks, msg.TaskID)
			delete(a.turns, msg.TaskID)
		}
		a.mu.Unlock()
		if ok && ch != nil {
			res := taskResult{Output: msg.Data, Result: msg.Payload, Turn: turn}
			ch <- res
			close(ch)
		}
		p.recordScanResultStats(a, msg.Payload)
		p.persistResultRecords(a, msg.TaskID, msg.Payload)

	case "error":
		a.mu.Lock()
		ch, ok := a.tasks[msg.TaskID]
		turn := a.turns[msg.TaskID]
		if ok {
			delete(a.tasks, msg.TaskID)
			delete(a.turns, msg.TaskID)
		}
		a.mu.Unlock()
		if ok && ch != nil {
			ch <- taskResult{Err: msg.Data, Turn: turn}
			close(ch)
		}

	default:
		// Backward-compat: flatten agent events into scan progress stream.
		if p.hub != nil && msg.TaskID != "" {
			raw, _ := json.Marshal(map[string]string{
				"scan_id": msg.TaskID,
				"data":    formatTelemetryProgress(msg),
			})
			p.hub.Broadcast(msg.TaskID, HubEvent{Type: "progress", Data: raw})
		}
		// Enriched: map agent events to typed ChatEvents for session SSE.
		p.forwardAgentEvent(a, msg)
		// Persist: write agent event as a record.
		p.persistAgentRecord(a, msg)
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

func (p *AgentPool) forwardToSession(a *remoteAgent, taskID string, event ChatEvent) {
	if p.sessions == nil || taskID == "" {
		return
	}
	sid, ok := p.sessions.TaskSession(taskID)
	if !ok {
		return
	}
	if event.AgentID == "" {
		event.AgentID = a.id
	}
	if event.AgentName == "" {
		event.AgentName = a.name
	}
	p.sessions.BroadcastChatEvent(sid, event)
}

func (p *AgentPool) forwardAgentEvent(a *remoteAgent, msg WSMessage) {
	if p.sessions == nil || msg.TaskID == "" {
		return
	}

	turn := agentEventTurn(msg.Payload)
	var event ChatEvent
	switch msg.Type {
	case "agent.turn_start":
		event = ChatEvent{Type: ChatEventThinking, Turn: turn, Transient: true}
	case "agent.message_start":
		role, content, _, ok := agentMessageFromPayload(msg.Payload)
		if !ok || role != "assistant" {
			return
		}
		event = ChatEvent{
			Type:    ChatEventMessageStart,
			Role:    role,
			Content: content,
			Turn:    turn,
		}
	case "agent.message_update":
		role, content, reasoning, ok := agentMessageFromPayload(msg.Payload)
		if !ok || role != "assistant" {
			return
		}
		if reasoning != "" {
			p.forwardToSession(a, msg.TaskID, ChatEvent{
				Type:      ChatEventThinking,
				Role:      role,
				Content:   reasoning,
				Turn:      turn,
				Transient: true,
			})
		}
		if content == "" {
			return
		}
		event = ChatEvent{
			Type:    ChatEventMessageDelta,
			Role:    role,
			Content: content,
			Turn:    turn,
		}
	case "agent.message_end":
		role, content, reasoning, ok := agentMessageFromPayload(msg.Payload)
		if !ok || role != "assistant" {
			return
		}
		if reasoning != "" {
			p.forwardToSession(a, msg.TaskID, ChatEvent{
				Type:    ChatEventThinking,
				Role:    role,
				Content: reasoning,
				Turn:    turn,
			})
		}
		if content == "" {
			return
		}
		event = ChatEvent{
			Type:    ChatEventMessageEnd,
			Role:    role,
			Content: content,
			Turn:    turn,
		}
	case "agent.tool_execution_start":
		var payload struct {
			ToolName   string `json:"tool_name"`
			ToolCallID string `json:"tool_call_id"`
			Arguments  string `json:"arguments"`
			Turn       int    `json:"turn"`
		}
		if msg.Payload != nil {
			_ = json.Unmarshal(msg.Payload, &payload)
		}
		if payload.Turn != 0 {
			turn = payload.Turn
		}
		event = ChatEvent{
			Type:       ChatEventToolCall,
			ToolName:   payload.ToolName,
			ToolArgs:   payload.Arguments,
			ToolCallID: payload.ToolCallID,
			Turn:       turn,
		}
	case "agent.tool_execution_end":
		var payload struct {
			ToolCallID string `json:"tool_call_id"`
			Result     string `json:"result"`
			Turn       int    `json:"turn"`
		}
		if msg.Payload != nil {
			_ = json.Unmarshal(msg.Payload, &payload)
		}
		if payload.Turn != 0 {
			turn = payload.Turn
		}
		event = ChatEvent{
			Type:       ChatEventToolResult,
			ToolCallID: payload.ToolCallID,
			Content:    payload.Result,
			Turn:       turn,
		}
	default:
		return
	}

	if turn > 0 {
		a.mu.Lock()
		if _, ok := a.tasks[msg.TaskID]; ok {
			a.turns[msg.TaskID] = turn
		}
		a.mu.Unlock()
	}

	p.forwardToSession(a, msg.TaskID, event)
}

func agentEventTurn(payload json.RawMessage) int {
	if len(payload) == 0 {
		return 0
	}
	var event struct {
		Turn int `json:"turn"`
	}
	_ = json.Unmarshal(payload, &event)
	return event.Turn
}

func agentMessageFromPayload(payload json.RawMessage) (role, content, reasoning string, ok bool) {
	if len(payload) == 0 {
		return "", "", "", false
	}
	var event struct {
		Message *struct {
			Role             string  `json:"role"`
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(payload, &event); err != nil || event.Message == nil {
		return "", "", "", false
	}
	role = event.Message.Role
	if event.Message.Content != nil {
		content = *event.Message.Content
	}
	if event.Message.ReasoningContent != nil {
		reasoning = *event.Message.ReasoningContent
	}
	return role, content, reasoning, role != ""
}

func (p *AgentPool) persistAgentRecord(a *remoteAgent, msg WSMessage) {
	if p.records == nil {
		return
	}
	rec := runner.WSPayloadToRecord(msg.Type, msg.TaskID, a.id, msg.Payload)
	if p.sessions != nil && msg.TaskID != "" {
		if sid, ok := p.sessions.TaskSession(msg.TaskID); ok {
			rec.SessionID = sid
		}
	}
	_ = p.records.InsertRecord(context.Background(), rec)
}

func (p *AgentPool) persistResultRecords(a *remoteAgent, taskID string, payload json.RawMessage) {
	if p.records == nil || len(payload) == 0 {
		return
	}
	var result output.Result
	if err := json.Unmarshal(payload, &result); err != nil {
		return
	}
	recs := runner.ResultToRecords(taskID, a.id, &result)
	if len(recs) > 0 {
		_ = p.records.InsertRecords(context.Background(), recs)
	}
}

func formatTelemetryProgress(msg WSMessage) string {
	if msg.Data == "" {
		return "[" + msg.Type + "]"
	}
	return fmt.Sprintf("[%s] %s", msg.Type, msg.Data)
}
