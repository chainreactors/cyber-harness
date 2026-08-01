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

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/core/output"
	terminalcodec "github.com/chainreactors/aiscan/pkg/web/terminal"
	"github.com/chainreactors/ioa/protocols"
	"github.com/chainreactors/utils/pty"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AgentInfo is the public view of a connected agent.
type AgentInfo struct {
	ID           string                   `json:"id"`
	Name         string                   `json:"name"`
	Commands     []string                 `json:"commands,omitempty"`
	CommandsMenu []*transport.CommandSpec `json:"commands_menu,omitempty"`
	Busy         bool                     `json:"busy"`
	ConnectAt    time.Time                `json:"connected_at"`
	Node         protocols.NodeRef        `json:"node"`
	Runtime      AgentRuntimeView         `json:"runtime,omitempty"`
	Status       AgentStatusView          `json:"status,omitempty"`
	Stats        AgentStatsView           `json:"stats,omitempty"`
}

type AgentRuntimeView struct {
	Hostname     string         `json:"hostname,omitempty"`
	Username     string         `json:"username,omitempty"`
	WorkingDir   string         `json:"working_dir,omitempty"`
	OS           string         `json:"os,omitempty"`
	Arch         string         `json:"arch,omitempty"`
	PID          int32          `json:"pid,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
	Meta         map[string]any `json:"meta,omitempty"`
}

type AgentStatusView struct {
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	Space       string `json:"space,omitempty"`
	Bound       bool   `json:"bound"`
	ConfigError string `json:"config_error,omitempty"`
}

type AgentStatsView struct {
	Turns            uint64 `json:"turns,omitempty"`
	ToolCalls        uint64 `json:"tool_calls,omitempty"`
	RunningTools     uint64 `json:"running_tools,omitempty"`
	PromptTokens     uint64 `json:"prompt_tokens,omitempty"`
	CompletionTokens uint64 `json:"completion_tokens,omitempty"`
	TotalTokens      uint64 `json:"total_tokens,omitempty"`
	CacheReadTokens  uint64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens uint64 `json:"cache_write_tokens,omitempty"`
	Assets           uint64 `json:"assets,omitempty"`
	Loots            uint64 `json:"loots,omitempty"`
	LastEvent        string `json:"last_event,omitempty"`
}

func cloneCommandSpecs(values []*transport.CommandSpec) []*transport.CommandSpec {
	if len(values) == 0 {
		return nil
	}
	out := make([]*transport.CommandSpec, 0, len(values))
	for _, value := range values {
		if value != nil {
			out = append(out, protobuf.Clone(value).(*transport.CommandSpec))
		}
	}
	return out
}

func runtimeView(value *transport.AgentRuntimeInfo) AgentRuntimeView {
	if value == nil {
		return AgentRuntimeView{}
	}
	view := AgentRuntimeView{
		Hostname: value.Hostname, Username: value.Username, WorkingDir: value.WorkingDir,
		OS: value.Os, Arch: value.Arch, PID: value.Pid, Capabilities: append([]string(nil), value.Capabilities...),
	}
	if value.Metadata != nil {
		view.Meta, _ = aop.DecodeJSON[map[string]any](value.Metadata)
	}
	return view
}

func statusView(value *transport.AgentStatus) AgentStatusView {
	if value == nil {
		return AgentStatusView{}
	}
	return AgentStatusView{Provider: value.Provider, Model: value.Model, Space: value.Space, Bound: value.Bound, ConfigError: value.ConfigError}
}

func statsView(value *transport.AgentStats) AgentStatsView {
	if value == nil {
		return AgentStatsView{}
	}
	return AgentStatsView{
		Turns: value.Turns, ToolCalls: value.ToolCalls, RunningTools: value.RunningTools,
		PromptTokens: value.InputTokens, CompletionTokens: value.OutputTokens, TotalTokens: value.TotalTokens,
		CacheReadTokens: value.CacheReadTokens, CacheWriteTokens: value.CacheWriteTokens,
		Assets: value.Assets, Loots: value.Loots, LastEvent: value.LastEvent,
	}
}

type taskResult struct {
	Output string
	Result json.RawMessage
	File   *transport.FileResult
	Err    string
	Turn   int
}

type remoteAgent struct {
	id           string
	name         string
	commands     []string
	commandsMenu []*transport.CommandSpec
	close        func()
	sendCh       chan *transport.ServerFrame
	controlCh    chan *transport.ServerFrame
	connectAt    time.Time
	node         protocols.NodeRef
	runtime      *transport.AgentRuntimeInfo
	status       *transport.AgentStatus
	stats        *transport.AgentStats

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
	reloadPending bool
	done          chan struct{}
}

func (a *remoteAgent) info() AgentInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AgentInfo{
		ID:           a.id,
		Name:         a.name,
		Commands:     a.commands,
		CommandsMenu: cloneCommandSpecs(a.commandsMenu),
		Busy:         len(a.tasks) > 0,
		ConnectAt:    a.connectAt,
		Node:         a.node,
		Runtime:      runtimeView(a.runtime),
		Status:       statusView(a.status),
		Stats:        statsView(a.stats),
	}
}

// commandSpecs returns the agent's reported "/verb" catalog (its agent-scope
// menu commands plus one per loaded skill). Immutable after register, so it
// needs no lock. The hub merges it with its hub-scope commands in SessionMenu.
func (a *remoteAgent) commandSpecs() []*transport.CommandSpec {
	if a == nil {
		return nil
	}
	return cloneCommandSpecs(a.commandsMenu)
}

// SessionLookup resolves a task ID to its owning chat session.
type SessionLookup interface {
	TaskSession(taskID string) (sessionID string, ok bool)
	BroadcastAOPEvent(sessionID string, event *aop.Event)
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
func agentKey(agentID, authority string) string {
	return (protocols.NodeRef{ID: agentID, Authority: authority}).URI()
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
		if old.close != nil {
			old.close()
		}
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

// DispatchToolCall sends a canonical AOP tool.call to a tool-capable node.
// The task completes only on the matching AOP tool.result.
func (p *AgentPool) DispatchToolCall(agentID, taskID string, call *aop.ToolCall) (<-chan taskResult, error) {
	a := p.get(agentID)
	if a == nil {
		return nil, fmt.Errorf("agent %s not connected", agentID)
	}
	call.Id = taskID
	sessionID := taskID
	if p.sessions != nil {
		if sid, ok := p.sessions.TaskSession(taskID); ok {
			sessionID = sid
		}
	}
	agentName := a.name
	if agentName == "" {
		agentName = a.id
	}
	event := &aop.Event{
		Id: generateID(), EmittedAt: timestamppb.Now(), SessionId: sessionID, TurnId: taskID, Emitter: agentName,
		Payload: &aop.Event_ToolCall{ToolCall: call},
	}
	a.mu.Lock()
	if a.toolCalls == nil {
		a.toolCalls = map[string]struct{}{}
	}
	a.toolCalls[taskID] = struct{}{}
	a.mu.Unlock()
	ch, err := p.dispatchFrame(agentID, taskID, &transport.ServerFrame{
		CorrelationId: taskID,
		Payload: &transport.ServerFrame_ToolCall{ToolCall: &transport.ToolCallRequest{
			TaskId: taskID, SessionId: sessionID, TurnId: taskID, Call: call,
		}},
	})
	if err != nil {
		a.mu.Lock()
		delete(a.toolCalls, taskID)
		a.mu.Unlock()
		return nil, err
	}
	if p.sessions != nil && sessionID != taskID {
		p.sessions.BroadcastAOPEvent(sessionID, event)
	}
	return ch, nil
}

// DispatchChat sends a natural-language prompt to an LLM-capable agent.
func (p *AgentPool) DispatchChat(agentID, taskID, prompt string) (<-chan taskResult, error) {
	return p.DispatchRun(agentID, &aop.RunTurnRequest{
		RequestId: taskID, TurnId: taskID,
		Input: &aop.Message{Role: "user", Content: []*aop.Content{{Value: &aop.Content_Text{Text: &aop.TextContent{Text: prompt}}}}},
	})
}

func (p *AgentPool) DispatchOpenSession(agentID string, request *aop.OpenSessionRequest) (<-chan taskResult, error) {
	if request == nil || strings.TrimSpace(request.RequestId) == "" || strings.TrimSpace(request.SessionId) == "" {
		return nil, fmt.Errorf("open session request_id and session_id are required")
	}
	return p.dispatchFrame(agentID, request.RequestId, &transport.ServerFrame{
		CorrelationId: request.RequestId,
		Payload:       &transport.ServerFrame_OpenSession{OpenSession: request},
	})
}

func (p *AgentPool) SessionOpen(agentID, sessionID string) bool {
	agent := p.get(agentID)
	if agent == nil {
		return false
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	_, ok := agent.openSessions[sessionID]
	return ok
}

func (p *AgentPool) DispatchCloseSession(agentID string, request *aop.CloseSessionRequest) (<-chan taskResult, error) {
	if request == nil || strings.TrimSpace(request.RequestId) == "" || strings.TrimSpace(request.SessionId) == "" {
		return nil, fmt.Errorf("close session request_id and session_id are required")
	}
	return p.dispatchFrame(agentID, request.RequestId, &transport.ServerFrame{
		CorrelationId: request.RequestId,
		Payload:       &transport.ServerFrame_CloseSession{CloseSession: request},
	})
}

func (p *AgentPool) DispatchRun(agentID string, request *aop.RunTurnRequest) (<-chan taskResult, error) {
	a := p.get(agentID)
	if a == nil {
		return nil, fmt.Errorf("agent %s not connected", agentID)
	}
	if request == nil || request.Input == nil || request.TurnId == "" {
		return nil, fmt.Errorf("run request with input and turn_id is required")
	}
	if request.SessionId != "" {
		a.mu.Lock()
		_, opened := a.openSessions[request.SessionId]
		if !opened {
			a.openSessions[request.SessionId] = struct{}{}
		}
		a.mu.Unlock()
		if !opened {
			select {
			case a.sendCh <- &transport.ServerFrame{CorrelationId: "open:" + request.SessionId, Payload: &transport.ServerFrame_OpenSession{OpenSession: &aop.OpenSessionRequest{
				RequestId: "open:" + request.SessionId, SessionId: request.SessionId, Participant: agentID,
			}}}:
			default:
				a.mu.Lock()
				delete(a.openSessions, request.SessionId)
				a.mu.Unlock()
				return nil, fmt.Errorf("agent %s send channel full", agentID)
			}
		}
	}
	return p.dispatchFrame(agentID, request.TurnId, &transport.ServerFrame{
		CorrelationId: request.TurnId, Payload: &transport.ServerFrame_RunTurn{RunTurn: request},
	})
}

func (p *AgentPool) DispatchCommand(agentID string, command *transport.CommandRequest) (<-chan taskResult, error) {
	if command == nil || command.TaskId == "" {
		return nil, fmt.Errorf("command task_id is required")
	}
	taskID := command.TaskId
	a := p.get(agentID)
	if a == nil {
		return nil, fmt.Errorf("agent %s not connected", agentID)
	}
	if command.SessionId != "" {
		a.mu.Lock()
		_, opened := a.openSessions[command.SessionId]
		if !opened {
			a.openSessions[command.SessionId] = struct{}{}
		}
		a.mu.Unlock()
		if !opened {
			select {
			case a.sendCh <- &transport.ServerFrame{CorrelationId: "open:" + command.SessionId, Payload: &transport.ServerFrame_OpenSession{OpenSession: &aop.OpenSessionRequest{
				RequestId: "open:" + command.SessionId, SessionId: command.SessionId, Participant: agentID,
			}}}:
			default:
				a.mu.Lock()
				delete(a.openSessions, command.SessionId)
				a.mu.Unlock()
				return nil, fmt.Errorf("agent %s send channel full", agentID)
			}
		}
	}
	return p.dispatchFrame(agentID, taskID, &transport.ServerFrame{
		CorrelationId: taskID,
		Payload:       &transport.ServerFrame_Command{Command: protobuf.Clone(command).(*transport.CommandRequest)},
	})
}

func (p *AgentPool) dispatchFrame(agentID, taskID string, frame *transport.ServerFrame) (<-chan taskResult, error) {
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
	case a.sendCh <- frame:
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
// Config notifications use the control channel so task output cannot starve a
// provider change. Repeated reloads are coalesced while one is queued or waiting
// for control-channel capacity; the agent always fetches the latest config.
func (p *AgentPool) BroadcastConfigReload() int {
	p.mu.RLock()
	agents := make([]*remoteAgent, 0, len(p.agents))
	for _, a := range p.agents {
		agents = append(agents, a)
	}
	p.mu.RUnlock()
	n := 0
	for _, a := range agents {
		if a.queueConfigReload() {
			n++
		}
	}
	return n
}

func (a *remoteAgent) queueConfigReload() bool {
	if a == nil || a.controlCh == nil {
		return false
	}
	a.mu.Lock()
	if a.reloadPending {
		a.mu.Unlock()
		return true
	}
	a.reloadPending = true
	a.mu.Unlock()

	frame := &transport.ServerFrame{Payload: &transport.ServerFrame_ReloadConfig{ReloadConfig: &transport.ReloadConfig{}}}
	select {
	case a.controlCh <- frame:
		return true
	default:
	}

	go func() {
		if a.done == nil {
			a.controlCh <- frame
			return
		}
		select {
		case a.controlCh <- frame:
		case <-a.done:
			a.mu.Lock()
			a.reloadPending = false
			a.mu.Unlock()
		}
	}()
	return true
}

func (a *remoteAgent) finishConfigReload() {
	a.mu.Lock()
	a.reloadPending = false
	a.mu.Unlock()
}

func (p *AgentPool) sendAgentFrame(agentID string, frame *transport.ServerFrame) error {
	a := p.get(agentID)
	if a == nil {
		return fmt.Errorf("agent %s not connected", agentID)
	}
	select {
	case a.sendCh <- frame:
		return nil
	default:
		return fmt.Errorf("agent %s send channel full", agentID)
	}
}

func (p *AgentPool) CancelTask(agentID, taskID string, sessionID ...string) error {
	a := p.get(agentID)
	if a == nil {
		return nil
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
	if !pending {
		return nil
	}
	var chatSessionID string
	if len(sessionID) > 0 {
		chatSessionID = sessionID[0]
	}
	cancelFrame := &transport.ServerFrame{CorrelationId: taskID, Payload: &transport.ServerFrame_CancelTurn{CancelTurn: &aop.CancelTurnRequest{
		RequestId: taskID, SessionId: chatSessionID, TurnId: taskID,
	}}}
	if isToolCall {
		cancelFrame = &transport.ServerFrame{CorrelationId: taskID, Payload: &transport.ServerFrame_CancelOperation{CancelOperation: &transport.CancelOperation{TaskId: taskID}}}
	}
	if resultCh != nil {
		close(resultCh)
	}
	a.enqueueControl(cancelFrame)
	return nil
}

// enqueueControl never drops a control frame because task traffic temporarily
// fills the channel. The pending send is bounded by the agent connection's
// lifetime and the writer always drains controlCh before sendCh.
func (a *remoteAgent) enqueueControl(frame *transport.ServerFrame) {
	if a == nil || a.controlCh == nil {
		return
	}
	select {
	case a.controlCh <- frame:
		return
	default:
	}
	go func() {
		if a.done == nil {
			a.controlCh <- frame
			return
		}
		select {
		case a.controlCh <- frame:
		case <-a.done:
		}
	}()
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
	events, online, unsubscribe := p.subscribePTY(agentID, terminalID)
	defer unsubscribe()
	defer p.CloseTerminal(agentID, terminalID)

	done := make(chan struct{})
	defer close(done)

	var writeMu sync.Mutex
	write := func(frame pty.Frame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		data, err := terminalcodec.Marshal(frame)
		if err != nil {
			return err
		}
		return conn.WriteMessage(websocket.TextMessage, data)
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
	if !online {
		_ = write(pty.Frame{Type: pty.FrameDetached, StreamID: terminalID})
	}

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		frame, err := terminalcodec.Unmarshal(data)
		if err != nil {
			_ = write(pty.Frame{Type: pty.FrameError, StreamID: terminalID, Error: "invalid terminal protobuf JSON: " + err.Error()})
			continue
		}
		if frame.Type == "" {
			_ = write(pty.Frame{Type: pty.FrameError, StreamID: terminalID, Error: "PTY frame type is required"})
			continue
		}
		frame.StreamID = terminalID
		if err := p.sendAgentFrame(agentID, &transport.ServerFrame{Payload: &transport.ServerFrame_Terminal{Terminal: terminalcodec.ToProto(frame)}}); err != nil {
			_ = write(pty.Frame{Type: pty.FrameError, StreamID: terminalID, Error: err.Error()})
			continue
		}
	}
}

func (p *AgentPool) CancelPTY(agentID, terminalID string) {
	_ = p.sendAgentFrame(agentID, &transport.ServerFrame{Payload: &transport.ServerFrame_Terminal{Terminal: terminalcodec.ToProto(pty.Frame{Type: pty.FrameKill, StreamID: terminalID})}})
}

func (p *AgentPool) CloseTerminal(agentID, terminalID string) {
	_ = p.sendAgentFrame(agentID, &transport.ServerFrame{Payload: &transport.ServerFrame_Terminal{Terminal: terminalcodec.ToProto(pty.Frame{Type: pty.FrameDetach, StreamID: terminalID})}})
}

func (p *AgentPool) subscribePTY(agentID, terminalID string) (<-chan pty.Frame, bool, func()) {
	ch := make(chan pty.Frame, 256)
	// Snapshot connectivity while registering the subscription under the pool
	// lock. An unregister cannot otherwise be distinguished from an initially
	// offline agent and can produce duplicate detached frames.
	p.mu.RLock()
	p.ptyMu.Lock()
	p.ptySubs[terminalID] = ch
	p.ptyAgents[terminalID] = agentID
	online := p.agents[agentID] != nil
	p.ptyMu.Unlock()
	p.mu.RUnlock()
	return ch, online, func() {
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
			case agent.sendCh <- &transport.ServerFrame{Payload: &transport.ServerFrame_Terminal{Terminal: terminalcodec.ToProto(pty.Frame{Type: pty.FrameList, StreamID: terminalID})}}:
			case <-agent.done:
			}
		}()
	}
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

func (p *AgentPool) recordScanResultStats(a *remoteAgent, payload json.RawMessage) {
	if a == nil || len(payload) == 0 {
		return
	}
	var result output.Result
	if err := json.Unmarshal(payload, &result); err != nil {
		return
	}
	a.mu.Lock()
	if a.stats == nil {
		a.stats = &transport.AgentStats{}
	}
	a.stats.Assets += uint64(len(result.Assets))
	if result.Summary.Loots > 0 {
		a.stats.Loots += uint64(result.Summary.Loots)
	} else {
		a.stats.Loots += uint64(len(result.Loots))
	}
	a.mu.Unlock()
}

// convergeTaskOnToolResult closes a tool.call task on its terminal
// tool.result: text content becomes the task output, structured Details the
// scan result, and an is_error content the task error. tool.result events of
// chat tasks (LLM tool use) are not terminals and pass through.
func (p *AgentPool) convergeTaskOnToolResult(a *remoteAgent, taskID string, ev *aop.Event) {
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
	d := ev.GetToolResult()
	res := taskResult{Output: aopToolResultText(d.Output), Turn: turn}
	if d.IsError {
		res.Err = res.Output
		res.Output = ""
	}
	var details json.RawMessage
	if d.Detail != nil {
		details = append(details, d.Detail.Data...)
		res.Result = details
	}
	ch <- res
	close(ch)
	p.recordScanResultStats(a, details)
	p.persistResultRecords(a, taskID, details)
}

// convergeTaskOnSessionEnd closes a chat task when the ROOT agent session
// ends: this terminal event drives task cleanup; child (derived sub-agent)
// session ends and mid-run AOP error events are not terminal. Idempotent —
// a file-RPC complete frame arriving after this close is a no-op.
func (p *AgentPool) convergeTaskOnTurnEnd(a *remoteAgent, taskID string, ev *aop.Event) {
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
	d := ev.GetTurnEnded()
	res := taskResult{Turn: turn}
	// A canceled run still carries the ctx error ("context canceled") — only
	// non-canceled stops surface it as a task error.
	if d.StopReason != "canceled" && d.Error != nil {
		res.Err = d.Error.Message
	}
	ch <- res
	close(ch)
}

func aopToolResultText(content []*aop.Content) string {
	var parts []string
	for _, item := range content {
		if text := item.GetText().GetText(); text != "" {
			parts = append(parts, text)
		} else if opaque := item.GetOpaque(); opaque != nil {
			parts = append(parts, string(opaque.Value.GetData()))
		}
	}
	return strings.Join(parts, "\n")
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
