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
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/ioa/protocols"
	"github.com/chainreactors/utils/pty"
	"github.com/gorilla/websocket"
)

// WSMessage is the single message type for all agent↔web communication.
type WSMessage = webproto.Message

// AgentInfo is the public view of a connected agent.
type AgentInfo struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Commands     []string               `json:"commands,omitempty"`
	CommandsMenu []webproto.CommandSpec `json:"commands_menu,omitempty"`
	Busy         bool                   `json:"busy"`
	ConnectAt    time.Time              `json:"connected_at"`
	Node         protocols.NodeRef      `json:"node"`
	Runtime      webproto.AgentRuntime  `json:"runtime,omitempty"`
	Status       webproto.AgentStatus   `json:"status,omitempty"`
	Stats        webproto.AgentStats    `json:"stats,omitempty"`
}

type taskResult struct {
	Output string
	Result json.RawMessage
	Err    string
	Turn   int
}

type remoteAgent struct {
	id           string
	name         string
	commands     []string
	commandsMenu []webproto.CommandSpec
	conn         *websocket.Conn
	sendCh       chan WSMessage
	controlCh    chan WSMessage
	connectAt    time.Time
	node         protocols.NodeRef
	runtime      webproto.AgentRuntime
	status       webproto.AgentStatus
	stats        webproto.AgentStats

	mu           sync.Mutex
	tasks        map[string]chan taskResult
	turns        map[string]int
	openSessions map[string]struct{}
	// toolCalls marks tasks dispatched via DispatchToolCall: only they converge
	// on a tool.result. Chat tasks see tool.result events too (LLM tool use)
	// and must ignore them as terminals.
	toolCalls map[string]struct{}
	// childSessions tracks derived sub-agent session IDs per task, learned from
	// session.start's parent_session_id. Only a ROOT session.end converges the
	// task; child ends are lifecycle noise.
	childSessions map[string]map[string]struct{}
	done          chan struct{}
}

func (a *remoteAgent) info() AgentInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AgentInfo{
		ID:           a.id,
		Name:         a.name,
		Commands:     a.commands,
		CommandsMenu: a.commandsMenu,
		Busy:         len(a.tasks) > 0,
		ConnectAt:    a.connectAt,
		Node:         a.node,
		Runtime:      a.runtime,
		Status:       a.status,
		Stats:        a.stats,
	}
}

// commandSpecs returns the agent's reported "/verb" catalog (its agent-scope
// menu commands plus one per loaded skill). Immutable after register, so it
// needs no lock. The hub merges it with its hub-scope commands in SessionMenu.
func (a *remoteAgent) commandSpecs() []webproto.CommandSpec {
	if a == nil {
		return nil
	}
	return a.commandsMenu
}

// SessionLookup resolves a task ID to its owning chat session.
type SessionLookup interface {
	TaskSession(taskID string) (sessionID string, ok bool)
	BroadcastChatEvent(sessionID string, event ChatEvent)
	BroadcastAOPEvent(sessionID string, event aop.Event)
}

// RecordStore is the subset of Store needed for record persistence.
type RecordStore interface {
	InsertRecord(ctx context.Context, rec *output.Record) error
	InsertRecords(ctx context.Context, recs []*output.Record) error
}

// SCOStore persists libcstx nodes emitted by a connected agent process.
type SCOStore interface {
	UpsertSCONodes(ctx context.Context, scanID string, nodes []json.RawMessage) error
}

// AgentPool manages connected remote aiscan agents via WebSocket.
type AgentPool struct {
	mu             sync.RWMutex
	agents         map[string]*remoteAgent
	hub            *Hub
	sessions       SessionLookup
	records        RecordStore
	sco            SCOStore
	ptyMu          sync.RWMutex
	ptySubs        map[string]chan pty.Frame
	ptyAgents      map[string]string
	ptyDrops       atomic.Int64
	allowedOrigins []string
	upgrader       websocket.Upgrader
}

func NewAgentPool(hub *Hub, allowedOrigins ...string) *AgentPool {
	return &AgentPool{
		agents:         make(map[string]*remoteAgent),
		hub:            hub,
		ptySubs:        make(map[string]chan pty.Frame),
		ptyAgents:      make(map[string]string),
		upgrader:       buildUpgrader(allowedOrigins),
		allowedOrigins: allowedOrigins,
	}
}

func (p *AgentPool) SetSessionLookup(sl SessionLookup) {
	p.sessions = sl
}

func (p *AgentPool) SetRecordStore(rs RecordStore) {
	p.records = rs
}

func (p *AgentPool) SetSCOStore(store SCOStore) {
	p.sco = store
}

// agentKey is the pool key for a registering agent: its canonical Web identity, so
// a reconnecting agent (WS flap, hub restart, config-driven bounce) re-registers
// under the SAME key. The hub used to mint a throwaway id per connection, which
// dangled every chat session bound to it — the session freezes the agent id at
// creation, so on reconnect the stored id resolved to nothing and the chat
// rejected every message as "not connected" even with the agent right back.
func agentKey(info webproto.RegisterPayload) string {
	return info.Node.URI()
}

func (p *AgentPool) register(a *remoteAgent) {
	p.mu.Lock()
	old := p.agents[a.id]
	p.agents[a.id] = a
	p.mu.Unlock()
	// The pool is keyed by stable identity (see agentKey), so a reconnecting agent
	// — or a second agent sharing the same node name — lands on an occupied slot.
	// Tear the stale connection down: its read loop then exits and its
	// identity-checked unregister no-ops, leaving `a` alone in the slot.
	if old != nil && old != a {
		_ = old.conn.Close()
	}
	p.rebindPTY(a)
}

func (p *AgentPool) unregister(a *remoteAgent) {
	p.mu.Lock()
	// Only vacate the slot if it still holds THIS instance. After a reconnect the
	// slot was already reassigned to the replacement under the same key; the old
	// instance tearing down must not evict its successor.
	removed := p.agents[a.id] == a
	if removed {
		delete(p.agents, a.id)
	}
	p.mu.Unlock()
	if removed {
		p.notifyPTY(a.id, pty.Frame{Type: pty.FrameDetached})
	}
	a.mu.Lock()
	for _, ch := range a.tasks {
		close(ch)
	}
	a.tasks = nil
	a.toolCalls = nil
	a.childSessions = nil
	a.mu.Unlock()
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
		chatCapable := a.status.Provider != ""
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

// DispatchToolCall sends a structured Command to a tool-capable node and
// returns a channel for the result. taskID correlates this non-Run RPC and its
// progress telemetry.
func (p *AgentPool) DispatchToolCall(agentID, taskID string, call aop.ToolCallData) (<-chan taskResult, error) {
	payload, err := json.Marshal(webproto.CommandPayload{SessionID: taskID, ToolCall: &call})
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}
	if a := p.get(agentID); a != nil {
		a.mu.Lock()
		if a.toolCalls == nil {
			a.toolCalls = map[string]struct{}{}
		}
		a.toolCalls[taskID] = struct{}{}
		a.mu.Unlock()
	}
	ch, err := p.dispatchMessage(agentID, taskID, WSMessage{Type: webproto.TypeCommand, TaskID: taskID, Payload: payload})
	if err != nil {
		if a := p.get(agentID); a != nil {
			a.mu.Lock()
			delete(a.toolCalls, taskID)
			a.mu.Unlock()
		}
		return nil, err
	}
	return ch, nil
}

// DispatchChat sends a natural-language prompt to an LLM-capable agent.
func (p *AgentPool) DispatchChat(agentID, taskID, prompt string) (<-chan taskResult, error) {
	return p.DispatchRun(agentID, taskID, webproto.RunPayload{Parts: []aop.MessagePart{{Type: aop.PartText, Text: prompt}}})
}

func (p *AgentPool) DispatchRun(agentID, runID string, run webproto.RunPayload) (<-chan taskResult, error) {
	a := p.get(agentID)
	if a == nil {
		return nil, fmt.Errorf("agent %s not connected", agentID)
	}
	if run.SessionID != "" {
		a.mu.Lock()
		_, opened := a.openSessions[run.SessionID]
		if !opened {
			a.openSessions[run.SessionID] = struct{}{}
		}
		a.mu.Unlock()
		if !opened {
			openPayload, _ := json.Marshal(webproto.SessionOpenPayload{SessionID: run.SessionID})
			select {
			case a.sendCh <- WSMessage{Type: webproto.TypeSessionOpen, Payload: openPayload}:
			default:
				a.mu.Lock()
				delete(a.openSessions, run.SessionID)
				a.mu.Unlock()
				return nil, fmt.Errorf("agent %s send channel full", agentID)
			}
		}
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return nil, fmt.Errorf("marshal run: %w", err)
	}
	return p.dispatchMessage(agentID, runID, WSMessage{Type: webproto.TypeRun, RunID: runID, Payload: payload})
}

func (p *AgentPool) DispatchCommand(agentID, taskID string, command webproto.CommandPayload) (<-chan taskResult, error) {
	a := p.get(agentID)
	if a == nil {
		return nil, fmt.Errorf("agent %s not connected", agentID)
	}
	if command.SessionID != "" {
		a.mu.Lock()
		_, opened := a.openSessions[command.SessionID]
		if !opened {
			a.openSessions[command.SessionID] = struct{}{}
		}
		a.mu.Unlock()
		if !opened {
			openPayload, _ := json.Marshal(webproto.SessionOpenPayload{SessionID: command.SessionID})
			select {
			case a.sendCh <- WSMessage{Type: webproto.TypeSessionOpen, Payload: openPayload}:
			default:
				a.mu.Lock()
				delete(a.openSessions, command.SessionID)
				a.mu.Unlock()
				return nil, fmt.Errorf("agent %s send channel full", agentID)
			}
		}
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}
	return p.dispatchMessage(agentID, taskID, WSMessage{Type: webproto.TypeCommand, TaskID: taskID, Payload: payload})
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

// BroadcastConfigReload notifies every connected agent that the hub config
// changed so each re-fetches and hot-swaps its LLM provider without a restart.
// Config notifications use a dedicated control channel so task output cannot
// starve or silently drop a provider change. A full channel already contains a
// pending reload, so the latest persisted config will still be fetched.
func (p *AgentPool) BroadcastConfigReload() int {
	p.mu.RLock()
	agents := make([]*remoteAgent, 0, len(p.agents))
	for _, a := range p.agents {
		agents = append(agents, a)
	}
	p.mu.RUnlock()
	n := 0
	for _, a := range agents {
		select {
		case a.controlCh <- WSMessage{Type: "config"}:
			n++
		default:
			// A pending config control frame already causes the agent to fetch the
			// newest persisted config, so this update is effectively coalesced.
			n++
		}
	}
	return n
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
	a.mu.Lock()
	resultCh, pending := a.tasks[taskID]
	_, isToolCall := a.toolCalls[taskID]
	if pending {
		delete(a.tasks, taskID)
		delete(a.turns, taskID)
		delete(a.toolCalls, taskID)
		delete(a.childSessions, taskID)
	}
	a.mu.Unlock()
	select {
	case a.sendCh <- func() WSMessage {
		if isToolCall {
			return WSMessage{Type: "cancel", TaskID: taskID}
		}
		return WSMessage{Type: webproto.TypeRunCancel, RunID: taskID}
	}():
	default:
	}
	if pending && resultCh != nil {
		close(resultCh)
	}
}

// HandleTerminalWS bridges one browser terminal WebSocket to one remote agent.
// The browser sends transport-neutral PTY frames; the pool assigns a stream_id,
// wraps them for the mixed agent connection, and unwraps matching responses.
func (p *AgentPool) HandleTerminalWS(agentID string, w http.ResponseWriter, r *http.Request) {
	conn, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	terminalID := generateID()
	events, unsubscribe := p.subscribePTY(agentID, terminalID)
	defer unsubscribe()
	defer p.CloseTerminal(agentID, terminalID)

	done := make(chan struct{})
	defer close(done)

	var writeMu sync.Mutex
	write := func(frame pty.Frame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(frame)
	}

	go func() {
		for {
			select {
			case msg, ok := <-events:
				if !ok {
					return
				}
				if err := write(msg); err != nil {
					_ = conn.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()
	if p.get(agentID) == nil {
		_ = write(pty.Frame{Type: pty.FrameDetached, StreamID: terminalID})
	}

	for {
		var frame pty.Frame
		if err := conn.ReadJSON(&frame); err != nil {
			return
		}
		if frame.Type == "" {
			_ = write(pty.Frame{Type: pty.FrameError, StreamID: terminalID, Error: "PTY frame type is required"})
			continue
		}
		frame.StreamID = terminalID
		if err := p.SendAgentMessage(agentID, webproto.NewPTYMessage(frame)); err != nil {
			_ = write(pty.Frame{Type: pty.FrameError, StreamID: terminalID, Error: err.Error()})
			continue
		}
	}
}

func (p *AgentPool) CancelPTY(agentID, terminalID string) {
	_ = p.SendAgentMessage(agentID, webproto.NewPTYMessage(pty.Frame{Type: pty.FrameKill, StreamID: terminalID}))
}

func (p *AgentPool) CloseTerminal(agentID, terminalID string) {
	_ = p.SendAgentMessage(agentID, webproto.NewPTYMessage(pty.Frame{Type: pty.FrameDetach, StreamID: terminalID}))
}

func (p *AgentPool) subscribePTY(agentID, terminalID string) (<-chan pty.Frame, func()) {
	ch := make(chan pty.Frame, 256)
	p.ptyMu.Lock()
	p.ptySubs[terminalID] = ch
	p.ptyAgents[terminalID] = agentID
	p.ptyMu.Unlock()
	return ch, func() {
		p.ptyMu.Lock()
		if p.ptySubs[terminalID] == ch {
			delete(p.ptySubs, terminalID)
			delete(p.ptyAgents, terminalID)
			close(ch)
		}
		p.ptyMu.Unlock()
	}
}

func (p *AgentPool) notifyPTY(agentID string, frame pty.Frame) {
	p.ptyMu.RLock()
	defer p.ptyMu.RUnlock()
	for terminalID, boundAgentID := range p.ptyAgents {
		if boundAgentID != agentID {
			continue
		}
		out := frame
		out.StreamID = terminalID
		if ch := p.ptySubs[terminalID]; ch != nil {
			select {
			case ch <- out:
			default:
				p.ptyDrops.Add(1)
			}
		}
	}
}

func (p *AgentPool) rebindPTY(agent *remoteAgent) {
	if agent == nil {
		return
	}
	p.ptyMu.RLock()
	terminalIDs := make([]string, 0)
	for terminalID, agentID := range p.ptyAgents {
		if agentID == agent.id {
			terminalIDs = append(terminalIDs, terminalID)
		}
	}
	p.ptyMu.RUnlock()
	for _, terminalID := range terminalIDs {
		terminalID := terminalID
		go func() {
			select {
			case agent.sendCh <- webproto.NewPTYMessage(pty.Frame{Type: pty.FrameList, StreamID: terminalID}):
			case <-agent.done:
			}
		}()
	}
}

func (p *AgentPool) forwardPTYMessage(msg WSMessage) bool {
	if msg.Type != webproto.TypePTY {
		return false
	}
	frame, err := webproto.DecodePTYMessage(msg)
	if err != nil || frame.StreamID == "" {
		return true
	}
	p.ptyMu.RLock()
	ch := p.ptySubs[frame.StreamID]
	if ch != nil {
		select {
		case ch <- frame:
		default:
			p.ptyDrops.Add(1)
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- frame:
			default:
				p.ptyDrops.Add(1)
			}
		}
	}
	p.ptyMu.RUnlock()
	return true
}

// --- WebSocket handler ---

func buildUpgrader(origins []string) websocket.Upgrader {
	if len(origins) == 0 {
		return websocket.Upgrader{}
	}
	return websocket.Upgrader{
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
	conn, err := p.upgrader.Upgrade(w, r, nil)
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
	// Resolve the stable pool key from the raw payload before the display-name
	// default below, so an anonymous client still gets a unique per-connection id
	// instead of every nameless agent colliding on the literal "agent".
	id := agentKey(info)
	if id == "" {
		conn.Close()
		return
	}
	if info.Name == "" {
		info.Name = "agent"
	}

	agent := &remoteAgent{
		id:            id,
		name:          info.Name,
		commands:      info.Commands,
		commandsMenu:  info.CommandsMenu,
		conn:          conn,
		sendCh:        make(chan WSMessage, 32),
		controlCh:     make(chan WSMessage, 1),
		connectAt:     time.Now(),
		node:          info.Node,
		runtime:       info.Runtime,
		status:        info.Status,
		stats:         info.Stats,
		tasks:         make(map[string]chan taskResult),
		turns:         make(map[string]int),
		openSessions:  make(map[string]struct{}),
		childSessions: make(map[string]map[string]struct{}),
		done:          make(chan struct{}),
	}
	p.register(agent)
	defer func() {
		p.unregister(agent)
		conn.Close()
		close(agent.done)
	}()

	// Send connected ack.
	ack, _ := json.Marshal(map[string]string{"agent_id": agent.id, "name": agent.name})
	if err := conn.WriteJSON(WSMessage{Type: "connected", Payload: ack}); err != nil {
		return
	}

	// Write goroutine: sendCh → WebSocket.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		closeBrokenConnection := func() {
			// A failed writer must tear down the shared WebSocket so the read
			// loop exits, unregisters this agent, and lets the client reconnect.
			// Otherwise the pool keeps a zombie "online" agent whose sendCh has
			// no consumer; PTY open/list requests then disappear indefinitely.
			_ = conn.Close()
		}
		for {
			// Give control frames priority over task/output traffic.
			select {
			case msg := <-agent.controlCh:
				if err := conn.WriteJSON(msg); err != nil {
					closeBrokenConnection()
					return
				}
				continue
			default:
			}
			select {
			case msg := <-agent.controlCh:
				if err := conn.WriteJSON(msg); err != nil {
					closeBrokenConnection()
					return
				}
			case msg, ok := <-agent.sendCh:
				if !ok {
					return
				}
				if err := conn.WriteJSON(msg); err != nil {
					closeBrokenConnection()
					return
				}
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					closeBrokenConnection()
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

	case "agent.status":
		var status webproto.AgentStatus
		if len(msg.Payload) > 0 && json.Unmarshal(msg.Payload, &status) == nil {
			a.mu.Lock()
			if status.Provider != "" {
				a.status.Provider = status.Provider
			}
			if status.Model != "" {
				a.status.Model = status.Model
			}
			a.status.Bound = status.Bound
			a.status.ConfigError = status.ConfigError
			if status.Space != "" {
				a.status.Space = status.Space
			}
			a.mu.Unlock()
		}

	case "tool.data":
		// Progress lines stream live to the scan/console topics; structured
		// scanner data persists through the libcstx-normalized tool.sco path.
		if p.hub == nil || msg.TaskID == "" {
			return
		}
		var event output.ToolDataEvent
		if json.Unmarshal(msg.Payload, &event) != nil || event.Kind != output.ToolDataProgress {
			return
		}
		line, ok := event.Data.(string)
		if !ok {
			return
		}
		data := output.StripANSI(line)
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

	case "tool.sco":
		if p.sco == nil || len(msg.Payload) == 0 {
			return
		}
		var payload struct {
			CallID string            `json:"call_id"`
			Nodes  []json.RawMessage `json:"nodes"`
		}
		if json.Unmarshal(msg.Payload, &payload) != nil || len(payload.Nodes) == 0 {
			return
		}
		scanID := payload.CallID
		if scanID == "" {
			scanID = msg.TaskID
		}
		if scanID == "" {
			scanID = "standalone"
		}
		_ = p.sco.UpsertSCONodes(context.Background(), scanID, payload.Nodes)

	case "config.result":
		var result webproto.ConfigReloadResult
		if len(msg.Payload) > 0 && json.Unmarshal(msg.Payload, &result) == nil {
			a.mu.Lock()
			if result.OK {
				a.status.Provider = result.Provider
				a.status.Model = result.Model
				a.status.ConfigError = ""
			} else {
				a.status.ConfigError = result.Error
			}
			a.mu.Unlock()
		}

	case webproto.TypeSessionOpened:
		var payload webproto.SessionLifecyclePayload
		if json.Unmarshal(msg.Payload, &payload) == nil && payload.SessionID != "" {
			a.mu.Lock()
			a.openSessions[payload.SessionID] = struct{}{}
			a.mu.Unlock()
		}

	case webproto.TypeSessionClosed:
		var payload webproto.SessionLifecyclePayload
		if json.Unmarshal(msg.Payload, &payload) == nil && payload.SessionID != "" {
			a.mu.Lock()
			delete(a.openSessions, payload.SessionID)
			a.mu.Unlock()
		}

	case webproto.TypeCommandResult:
		a.mu.Lock()
		ch, ok := a.tasks[msg.TaskID]
		_, isToolCall := a.toolCalls[msg.TaskID]
		if ok {
			delete(a.tasks, msg.TaskID)
			delete(a.turns, msg.TaskID)
			delete(a.toolCalls, msg.TaskID)
		}
		a.mu.Unlock()
		if ok && ch != nil {
			result := taskResult{Result: msg.Payload}
			if isToolCall {
				var command webproto.CommandResultPayload
				if err := json.Unmarshal(msg.Payload, &command); err != nil {
					result.Err = "decode command.result: " + err.Error()
				} else {
					result.Output = commandPartsText(command.Parts)
					if isError, _ := command.Metadata["is_error"].(bool); isError {
						result.Err, result.Output = result.Output, ""
					}
					if details := command.Metadata["details"]; details != nil {
						result.Result, _ = json.Marshal(details)
					}
				}
			}
			ch <- result
			close(ch)
			if isToolCall {
				p.recordScanResultStats(a, result.Result)
				p.persistResultRecords(a, msg.TaskID, result.Result)
			}
		}

	// complete/error are the terminal envelopes of the file RPCs only; agent
	// semantics (chat, tool calls) converge on AOP events.
	case "complete":
		a.mu.Lock()
		ch, ok := a.tasks[msg.TaskID]
		turn := a.turns[msg.TaskID]
		if ok {
			delete(a.tasks, msg.TaskID)
			delete(a.turns, msg.TaskID)
			delete(a.toolCalls, msg.TaskID)
			delete(a.childSessions, msg.TaskID)
		}
		a.mu.Unlock()
		if ok && ch != nil {
			res := taskResult{Output: msg.Data, Result: msg.Payload, Turn: turn}
			ch <- res
			close(ch)
		}

	case "error":
		correlationID := msg.RunID
		if correlationID == "" {
			correlationID = msg.TaskID
		}
		a.mu.Lock()
		ch, ok := a.tasks[correlationID]
		turn := a.turns[correlationID]
		if ok {
			delete(a.tasks, correlationID)
			delete(a.turns, correlationID)
			delete(a.toolCalls, correlationID)
			delete(a.childSessions, correlationID)
		}
		a.mu.Unlock()
		if ok && ch != nil {
			var payload webproto.ErrorPayload
			errText := msg.Data
			if json.Unmarshal(msg.Payload, &payload) == nil && payload.Message != "" {
				errText = payload.Message
			}
			ch <- taskResult{Err: errText, Turn: turn}
			close(ch)
		}

	case "aop":
		// The transport identifies only the protocol. Event semantics live in
		// the untouched AOP payload and are validated once at this ingress.
		p.forwardAOPEvent(a, msg)

	default:
		// Unknown control frames are intentionally not projected into another
		// protocol. Producers must emit either a documented control frame or AOP.
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

func (p *AgentPool) forwardAOPEvent(a *remoteAgent, msg WSMessage) {
	var aopEv aop.Event
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &aopEv)
	}
	if !aopEv.Valid() {
		return
	}
	// Session-topic broadcast is optional (scans dispatched outside chat have
	// no chat session); task convergence below is not.
	if p.sessions != nil {
		if sid, ok := p.sessions.TaskSession(msg.RunID); ok {
			p.sessions.BroadcastAOPEvent(sid, aopEv)
		} else if aopEv.SessionID != "" {
			p.sessions.BroadcastAOPEvent(aopEv.SessionID, aopEv)
		}
	}

	switch aopEv.Type {
	case aop.TypeTurnEnd:
		p.convergeTaskOnTurnEnd(a, msg.RunID, aopEv)

	case aop.TypeToolResult:
		p.convergeTaskOnToolResult(a, msg.TaskID, aopEv)
	}

}

// convergeTaskOnToolResult closes a tool.call task on its terminal
// tool.result: text content becomes the task output, structured Details the
// scan result, and an is_error content the task error. tool.result events of
// chat tasks (LLM tool use) are not terminals and pass through.
func (p *AgentPool) convergeTaskOnToolResult(a *remoteAgent, taskID string, ev aop.Event) {
	a.mu.Lock()
	if _, isToolCall := a.toolCalls[taskID]; !isToolCall {
		a.mu.Unlock()
		return
	}
	ch, ok := a.tasks[taskID]
	turn := a.turns[taskID]
	if ok {
		delete(a.tasks, taskID)
		delete(a.turns, taskID)
		delete(a.toolCalls, taskID)
		delete(a.childSessions, taskID)
	}
	a.mu.Unlock()
	if !ok || ch == nil {
		return
	}
	var d aop.ToolResultData
	if err := json.Unmarshal(ev.Data, &d); err != nil {
		ch <- taskResult{Err: "decode tool.result: " + err.Error(), Turn: turn}
		close(ch)
		return
	}
	res := taskResult{Output: toolResultText(d.Content), Turn: turn}
	if d.IsError {
		res.Err = res.Output
		res.Output = ""
	}
	var details json.RawMessage
	if d.Details != nil {
		details, _ = json.Marshal(d.Details)
		res.Result = details
	}
	ch <- res
	close(ch)
	p.recordScanResultStats(a, details)
	p.persistResultRecords(a, taskID, details)
}

// toolResultText flattens tool.result content: a plain string, or the text of
// a structured ToolResultContent (text plus images).
func toolResultText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case map[string]any:
		if text, ok := c["content"].(string); ok {
			return text
		}
	}
	return ""
}

func commandPartsText(parts []aop.MessagePart) string {
	var values []string
	for _, part := range parts {
		if part.Type == aop.PartText && part.Text != "" {
			values = append(values, part.Text)
		}
	}
	return strings.Join(values, "\n")
}

// convergeTaskOnSessionEnd closes a chat task when the ROOT agent session
// ends: this terminal event drives task cleanup; child (derived sub-agent)
// session ends and mid-run AOP error events are not terminal. Idempotent —
// a file-RPC complete frame arriving after this close is a no-op.
func (p *AgentPool) convergeTaskOnTurnEnd(a *remoteAgent, taskID string, ev aop.Event) {
	if taskID == "" {
		return
	}
	a.mu.Lock()
	ch, ok := a.tasks[taskID]
	turn := a.turns[taskID]
	if ok {
		delete(a.tasks, taskID)
		delete(a.turns, taskID)
		delete(a.toolCalls, taskID)
		delete(a.childSessions, taskID)
	}
	a.mu.Unlock()
	if !ok || ch == nil {
		return
	}
	var d aop.TurnEndData
	_ = json.Unmarshal(ev.Data, &d)
	res := taskResult{Turn: turn}
	// A canceled run still carries the ctx error ("context canceled") — only
	// non-canceled stops surface it as a task error.
	if d.Stop != "canceled" && d.Error != "" {
		res.Err = d.Error
	}
	ch <- res
	close(ch)
}

func (p *AgentPool) persistResultRecords(a *remoteAgent, taskID string, payload json.RawMessage) {
	if p.records == nil || len(payload) == 0 {
		return
	}
	var result output.Result
	if err := json.Unmarshal(payload, &result); err != nil {
		return
	}
	recs := resultToRecords(taskID, a.id, &result)
	if len(recs) > 0 {
		_ = p.records.InsertRecords(context.Background(), recs)
	}
}

func resultToRecords(scanID, agentID string, result *output.Result) []*output.Record {
	if result == nil {
		return nil
	}
	var recs []*output.Record
	now := time.Now()
	for _, loot := range result.Loots {
		rec := &output.Record{
			Timestamp: now,
			Loot:      true,
			ID:        generateID(),
			ScanID:    scanID,
			AgentID:   agentID,
			Source:    loot.Kind,
			Target:    loot.Target,
			Priority:  loot.Priority,
			Summary:   loot.Description,
			Tags:      loot.Tags,
		}
		switch loot.Kind {
		case output.LootVuln:
			rec.Type = output.TypeNeutron
		case output.LootWeakpass:
			rec.Type = output.TypeZombie
		case output.LootFingerprint:
			rec.Type = output.TypeGogo
		default:
			rec.Type = output.RecordType(loot.Kind)
		}
		data, _ := json.Marshal(loot)
		rec.Data = data
		recs = append(recs, rec)
	}
	for _, e := range result.Errors {
		data, _ := json.Marshal(e)
		recs = append(recs, &output.Record{
			Type:      output.TypeError,
			Timestamp: now,
			Data:      data,
			ID:        generateID(),
			ScanID:    scanID,
			AgentID:   agentID,
			Source:    e.Source,
			Summary:   e.Message,
		})
	}
	return recs
}
