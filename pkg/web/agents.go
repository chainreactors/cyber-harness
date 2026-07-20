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
	connectAt    time.Time
	node         protocols.NodeRef
	runtime      webproto.AgentRuntime
	status       webproto.AgentStatus
	stats        webproto.AgentStats

	mu    sync.Mutex
	tasks map[string]chan taskResult
	turns map[string]int
	done  chan struct{}
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
	ptySubs        map[string]chan WSMessage
	ptyDrops       atomic.Int64
	allowedOrigins []string
	upgrader       websocket.Upgrader
}

func NewAgentPool(hub *Hub, allowedOrigins ...string) *AgentPool {
	return &AgentPool{
		agents:         make(map[string]*remoteAgent),
		hub:            hub,
		ptySubs:        make(map[string]chan WSMessage),
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
}

func (p *AgentPool) unregister(a *remoteAgent) {
	p.mu.Lock()
	// Only vacate the slot if it still holds THIS instance. After a reconnect the
	// slot was already reassigned to the replacement under the same key; the old
	// instance tearing down must not evict its successor.
	if p.agents[a.id] == a {
		delete(p.agents, a.id)
	}
	p.mu.Unlock()
	a.mu.Lock()
	for _, ch := range a.tasks {
		close(ch)
	}
	a.tasks = nil
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

// DispatchCommand sends a command to an agent and returns a channel for the result.
func (p *AgentPool) DispatchCommand(agentID, taskID, command string) (<-chan taskResult, error) {
	return p.dispatchPayload(agentID, taskID, "exec", command, nil)
}

// DispatchChat sends a natural-language prompt to an LLM-capable agent.
func (p *AgentPool) DispatchChat(agentID, taskID, prompt string) (<-chan taskResult, error) {
	return p.DispatchChatSession(agentID, taskID, "", prompt, webproto.ChatPayload{})
}

// DispatchChatSession sends chat input to an agent and scopes the remote
// agent-side conversation state to the web chat session. Goal-mode controls in
// opts (persist / eval criteria / turn caps) ride along so the agent can run
// the evaluator loop instead of a plain single-shot turn.
func (p *AgentPool) DispatchChatSession(agentID, taskID, sessionID, prompt string, opts webproto.ChatPayload) (<-chan taskResult, error) {
	opts.SessionID = sessionID
	return p.dispatchPayload(agentID, taskID, "chat", prompt, mustJSON(opts))
}

func (p *AgentPool) dispatchPayload(agentID, taskID, typ, data string, payload json.RawMessage) (<-chan taskResult, error) {
	return p.dispatchMessage(agentID, taskID, WSMessage{Type: typ, TaskID: taskID, Data: data, Payload: payload})
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
// Best-effort: an agent whose send channel is full picks the change up on its
// next reconnect. Returns the number of agents notified.
func (p *AgentPool) BroadcastConfigReload() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	n := 0
	for _, a := range p.agents {
		select { // non-blocking, so safe to send under the read lock
		case a.sendCh <- WSMessage{Type: "config"}:
			n++
		default:
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

	conn, err := p.upgrader.Upgrade(w, r, nil)
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
		id:           id,
		name:         info.Name,
		commands:     info.Commands,
		commandsMenu: info.CommandsMenu,
		conn:         conn,
		sendCh:       make(chan WSMessage, 32),
		connectAt:    time.Now(),
		node:         info.Node,
		runtime:      info.Runtime,
		status:       info.Status,
		stats:        info.Stats,
		tasks:        make(map[string]chan taskResult),
		turns:        make(map[string]int),
		done:         make(chan struct{}),
	}
	p.register(agent)
	defer func() {
		p.unregister(agent)
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
			if status.Space != "" {
				a.status.Space = status.Space
			}
			a.mu.Unlock()
		}

	case "tool.data":
		// Raw typed scanner data is transported for live consumers. The durable
		// asset path uses the corresponding libcstx-normalized tool.sco message.

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

	case "output":
		if p.hub != nil && msg.TaskID != "" {
			data := output.StripANSI(msg.Data)
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
	if p.sessions == nil || msg.TaskID == "" {
		return
	}

	var aopEv aop.Event
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &aopEv)
	}
	if !aopEv.Valid() {
		return
	}
	if sid, ok := p.sessions.TaskSession(msg.TaskID); ok {
		p.sessions.BroadcastAOPEvent(sid, aopEv)
	}

	ext := extractAgentExt(aopEv)
	if aopEv.Type == aop.TypeTurnStart {
		var d aop.TurnData
		_ = json.Unmarshal(aopEv.Data, &d)
		if d.Turn > 0 {
			a.mu.Lock()
			if _, ok := a.tasks[msg.TaskID]; ok {
				a.turns[msg.TaskID] = d.Turn
			}
			a.mu.Unlock()
		}
	}

	// Evaluator/compaction metadata belongs to the web product, so it remains a
	// platform control event while the underlying agent event stays untouched.
	if ext != nil {
		if round, ok := ext["eval_round"].(float64); ok && round > 0 {
			pass, _ := ext["eval_pass"].(bool)
			reason, _ := ext["eval_reason"].(string)
			if errMsg, ok := ext["eval_error"].(string); ok && errMsg != "" {
				reason = errMsg
			}
			p.forwardToSession(a, msg.TaskID, ChatEvent{
				Type:       ChatEventEval,
				EvalRound:  int(round),
				EvalPass:   pass,
				EvalReason: reason,
			})
		}
		if before, ok := ext["compact_tokens_before"].(float64); ok && before > 0 {
			after, _ := ext["compact_tokens_after"].(float64)
			kept, _ := ext["compact_kept_messages"].(float64)
			p.forwardToSession(a, msg.TaskID, ChatEvent{
				Type:                ChatEventCompact,
				CompactTokensBefore: int(before),
				CompactTokensAfter:  int(after),
				CompactKeptMessages: int(kept),
			})
		}
	}
}

func extractAgentExt(ev aop.Event) map[string]any {
	if ev.Ext == nil {
		return nil
	}
	// Try the agent name from the event first, then any namespace.
	if ext, ok := ev.Ext[ev.Agent]; ok {
		if m, ok := ext.(map[string]any); ok {
			return m
		}
	}
	for _, ext := range ev.Ext {
		if m, ok := ext.(map[string]any); ok {
			return m
		}
	}
	return nil
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
