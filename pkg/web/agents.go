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
	filepb "github.com/chainreactors/aiscan/aop/file"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	types "github.com/chainreactors/aiscan/pkg/types"
	terminalcodec "github.com/chainreactors/aiscan/pkg/web/terminal"
	"github.com/chainreactors/utils/pty"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func cloneCommandSpecs(values []*types.CommandSpec) []*types.CommandSpec {
	if len(values) == 0 {
		return nil
	}
	out := make([]*types.CommandSpec, 0, len(values))
	for _, value := range values {
		if value != nil {
			out = append(out, protobuf.Clone(value).(*types.CommandSpec))
		}
	}
	return out
}

type taskResult struct {
	Output string
	File   *filepb.Result
	Err    string
	Turn   int
}

// nodeState is the per-node task/session bookkeeping shared by both pool
// member kinds. tasks map in-flight operation IDs to their waiter channels;
// toolCalls marks tasks dispatched via DispatchToolCall: only they converge
// on a tool.result. Chat tasks see tool.result events too (LLM tool use)
// and must ignore them as terminals. childSessions tracks derived sub-agent
// session IDs per task: only a ROOT session.end converges the task.
type nodeState struct {
	mu            sync.Mutex
	tasks         map[string]chan taskResult
	turns         map[string]int
	openSessions  map[string]struct{}
	toolCalls     map[string]struct{}
	childSessions map[string]map[string]struct{}
}

func newNodeState() *nodeState {
	return &nodeState{
		tasks:         make(map[string]chan taskResult),
		turns:         make(map[string]int),
		openSessions:  make(map[string]struct{}),
		toolCalls:     make(map[string]struct{}),
		childSessions: make(map[string]map[string]struct{}),
	}
}

func (s *nodeState) busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tasks) > 0
}

func (s *nodeState) sessionOpen(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.openSessions[sessionID]
	return ok
}

// finishTask delivers result to the waiter of taskID and clears its
// bookkeeping. Idempotent — a late frame after convergence is a no-op.
func (s *nodeState) finishTask(taskID string, result taskResult) {
	if taskID == "" {
		return
	}
	s.mu.Lock()
	ch, ok := s.tasks[taskID]
	result.Turn = s.turns[taskID]
	if ok {
		delete(s.tasks, taskID)
		delete(s.turns, taskID)
		delete(s.toolCalls, taskID)
		delete(s.childSessions, taskID)
	}
	s.mu.Unlock()
	if ok && ch != nil {
		ch <- result
		close(ch)
	}
}

// dropTask clears taskID's bookkeeping and closes its waiter with no result.
func (s *nodeState) dropTask(taskID string) (chan taskResult, bool) {
	s.mu.Lock()
	ch, pending := s.tasks[taskID]
	if pending {
		delete(s.tasks, taskID)
		delete(s.turns, taskID)
		delete(s.toolCalls, taskID)
		delete(s.childSessions, taskID)
	}
	s.mu.Unlock()
	return ch, pending
}

// convergeOnToolResult closes a tool.call task on its terminal tool.result.
// SCO facts travel independently through the SCO namespace; tool.result
// carries only operation completion and human-readable output.
func (s *nodeState) convergeOnToolResult(taskID string, ev *aop.Event) {
	s.mu.Lock()
	if _, isToolCall := s.toolCalls[taskID]; !isToolCall {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	d := ev.GetToolResult()
	res := taskResult{Output: aopToolResultText(d.Output)}
	if d.IsError {
		res.Err = res.Output
		res.Output = ""
	}
	s.finishTask(taskID, res)
}

// convergeOnTurnEnd closes a chat task when the ROOT agent session ends.
// A canceled run still carries the ctx error ("context canceled") — only
// non-canceled stops surface it as a task error.
func (s *nodeState) convergeOnTurnEnd(taskID string, ev *aop.Event) {
	if taskID == "" {
		return
	}
	d := ev.GetTurnEnded()
	res := taskResult{}
	if d.StopReason != "canceled" && d.Error != nil {
		res.Err = d.Error.Message
	}
	s.finishTask(taskID, res)
}

func (s *nodeState) closeAllTasks() {
	s.mu.Lock()
	for _, ch := range s.tasks {
		close(ch)
	}
	s.tasks = nil
	s.toolCalls = nil
	s.childSessions = nil
	s.mu.Unlock()
}

type remoteAgent struct {
	*nodeState
	nodeID       string
	name         string
	capabilities []string
	commandsMenu []*types.CommandSpec
	close        func()
	sendCh       chan *aop.Envelope
	connectAt    time.Time
	runtime      *aop.AgentRuntimeInfo
	status       *aop.AgentStatus
	stats        *aop.AgentStats

	done chan struct{}
}

func (a *remoteAgent) NodeID() string    { return a.nodeID }
func (a *remoteAgent) Name() string      { return a.name }
func (a *remoteAgent) state() *nodeState { return a.nodeState }
func (a *remoteAgent) shutdown()         { a.close() }
func (a *remoteAgent) chatCapable() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status != nil && a.status.Provider != ""
}

func (a *remoteAgent) reloadConfig(config *types.DistributeConfig) {
	message := &types.ReloadProtocolMessage{Message: &types.ReloadProtocolMessage_Request{Request: &types.ReloadRequest{Config: protobuf.Clone(config).(*types.DistributeConfig)}}}
	if envelope, err := aop.Wrap(generateID(), "", message); err == nil {
		_ = a.enqueue(envelope)
	}
}

func (a *remoteAgent) view() *types.AgentView {
	a.mu.Lock()
	defer a.mu.Unlock()
	hello := &aop.AgentHello{
		NodeId:       a.nodeID,
		Name:         a.name,
		Capabilities: append([]string(nil), a.capabilities...),
	}
	if a.runtime != nil {
		hello.Runtime = protobuf.Clone(a.runtime).(*aop.AgentRuntimeInfo)
	}
	view := &types.AgentView{Hello: hello, ConnectedAt: timestamppb.New(a.connectAt), Commands: cloneCommandSpecs(a.commandsMenu), Busy: len(a.tasks) > 0}
	if a.status != nil {
		view.Status = protobuf.Clone(a.status).(*aop.AgentStatus)
	}
	if a.stats != nil {
		view.Stats = protobuf.Clone(a.stats).(*aop.AgentStats)
	}
	return view
}

// commandSpecs returns the agent's reported "/verb" catalog (its agent-scope
// menu commands plus one per loaded skill). Immutable after register, so it
// needs no lock. The hub merges it with its hub-scope commands in SessionMenu.
func (a *remoteAgent) commandSpecs() []*types.CommandSpec {
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

// SCOStore persists libcstx nodes and records which AOP operation observed
// them. Node identity is global; operation membership is many-to-many.
type SCOStore interface {
	UpsertSCONodes(ctx context.Context, operationID string, nodes []json.RawMessage) error
}

// AgentPool manages connected aiscan agent nodes. Every member is a node that
// registered over the application WebSocket — including the hub's own embedded
// agent, which connects over loopback like any other node.
type AgentPool struct {
	mu             sync.RWMutex
	agents         map[string]*remoteAgent
	hub            *Hub
	sessions       SessionLookup
	sco            SCOStore
	config         func(context.Context) (*types.DistributeConfig, error)
	ptyMu          sync.RWMutex
	ptySubs        map[string]chan pty.Frame
	ptyNodeIDs     map[string]string
	ptyDrops       atomic.Int64
	allowedOrigins []string
	upgrader       websocket.Upgrader
}

func NewAgentPool(hub *Hub, allowedOrigins ...string) *AgentPool {
	return &AgentPool{
		agents:         make(map[string]*remoteAgent),
		hub:            hub,
		ptySubs:        make(map[string]chan pty.Frame),
		ptyNodeIDs:     make(map[string]string),
		upgrader:       buildUpgrader(allowedOrigins),
		allowedOrigins: allowedOrigins,
	}
}

func (p *AgentPool) SetSessionLookup(sl SessionLookup) {
	p.sessions = sl
}

func (p *AgentPool) SetSCOStore(store SCOStore) {
	p.sco = store
}

func (p *AgentPool) register(a *remoteAgent) {
	p.mu.Lock()
	old := p.agents[a.NodeID()]
	p.agents[a.NodeID()] = a
	p.mu.Unlock()
	// The pool is keyed directly by node_id, so a reconnecting node lands on the
	// same slot instead of creating a connection-scoped identity.
	// Tear the stale connection down: its read loop then exits and its
	// identity-checked unregister no-ops, leaving `a` alone in the slot.
	if old != nil && old != a {
		old.shutdown()
	}
	p.rebindPTY(a)
}

func (p *AgentPool) unregister(a *remoteAgent) {
	p.mu.Lock()
	// Only vacate the slot if it still holds THIS instance. After a reconnect the
	// slot was already reassigned to the replacement under the same key; the old
	// instance tearing down must not evict its successor.
	removed := p.agents[a.NodeID()] == a
	if removed {
		delete(p.agents, a.NodeID())
	}
	p.mu.Unlock()
	if removed {
		p.notifyPTY(a.NodeID(), pty.Frame{Type: pty.FrameDetached})
	}
	a.state().closeAllTasks()
}

func (p *AgentPool) get(nodeID string) *remoteAgent {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.agents[nodeID]
}

func (p *AgentPool) List() []*types.AgentView {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*types.AgentView, 0, len(p.agents))
	for _, a := range p.agents {
		out = append(out, a.view())
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
		if !a.state().busy() {
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
		if !a.chatCapable() {
			continue
		}
		if !a.state().busy() {
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
func (p *AgentPool) DispatchToolCall(nodeID, taskID string, call *aop.ToolCall) (<-chan taskResult, error) {
	a := p.get(nodeID)
	if a == nil {
		return nil, fmt.Errorf("node %s not connected", nodeID)
	}
	call.Id = taskID
	sessionID := taskID
	if p.sessions != nil {
		if sid, ok := p.sessions.TaskSession(taskID); ok {
			sessionID = sid
		}
	}
	agentName := a.Name()
	if agentName == "" {
		agentName = a.NodeID()
	}
	event := &aop.Event{
		Id: generateID(), EmittedAt: timestamppb.Now(), SessionId: sessionID, TurnId: taskID, Emitter: agentName,
		Payload: &aop.Event_ToolCall{ToolCall: call},
	}
	st := a.state()
	st.mu.Lock()
	st.toolCalls[taskID] = struct{}{}
	st.mu.Unlock()
	ch, err := p.dispatchMessage(nodeID, taskID, &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{Call: &toolpb.Call{
		SessionId: sessionID, TurnId: taskID, Call: call,
	}}})
	if err != nil {
		st.mu.Lock()
		delete(st.toolCalls, taskID)
		st.mu.Unlock()
		return nil, err
	}
	if p.sessions != nil && sessionID != taskID {
		p.sessions.BroadcastAOPEvent(sessionID, event)
	}
	return ch, nil
}

// DispatchChat sends a natural-language prompt to an LLM-capable agent.
func (p *AgentPool) DispatchChat(nodeID, taskID, prompt string) (<-chan taskResult, error) {
	return p.DispatchRun(nodeID, &aop.RunTurnRequest{
		TurnId: taskID,
		Input:  &aop.Message{Role: "user", Content: []*aop.Content{{Value: &aop.Content_Text{Text: &aop.TextContent{Text: prompt}}}}},
	})
}

func (p *AgentPool) DispatchOpenSession(nodeID, requestID string, request *aop.OpenSessionRequest) (<-chan taskResult, error) {
	if request == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(request.SessionId) == "" {
		return nil, fmt.Errorf("open session envelope id and session_id are required")
	}
	return p.dispatchMessage(nodeID, requestID, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{OpenSessionRequest: request}})
}

func (p *AgentPool) SessionOpen(nodeID, sessionID string) bool {
	agent := p.get(nodeID)
	if agent == nil {
		return false
	}
	return agent.state().sessionOpen(sessionID)
}

func (p *AgentPool) DispatchCloseSession(nodeID, requestID string, request *aop.CloseSessionRequest) (<-chan taskResult, error) {
	if request == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(request.SessionId) == "" {
		return nil, fmt.Errorf("close session envelope id and session_id are required")
	}
	return p.dispatchMessage(nodeID, requestID, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionRequest{CloseSessionRequest: request}})
}

// ensureSessionOpen optimistically marks sessionID open on the node and sends
// the OpenSessionRequest once. Used by DispatchRun/DispatchCommand, which can
// arrive on a session the hub never explicitly opened.
func (p *AgentPool) ensureSessionOpen(a *remoteAgent, sessionID string) error {
	st := a.state()
	st.mu.Lock()
	_, opened := st.openSessions[sessionID]
	if !opened {
		st.openSessions[sessionID] = struct{}{}
	}
	st.mu.Unlock()
	if opened {
		return nil
	}
	requestID := "open:" + sessionID
	if err := p.sendAgentMessage(a.NodeID(), requestID, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{OpenSessionRequest: &aop.OpenSessionRequest{
		SessionId: sessionID, NodeId: a.NodeID(),
	}}}); err != nil {
		st.mu.Lock()
		delete(st.openSessions, sessionID)
		st.mu.Unlock()
		return err
	}
	return nil
}

func (p *AgentPool) DispatchRun(nodeID string, request *aop.RunTurnRequest) (<-chan taskResult, error) {
	a := p.get(nodeID)
	if a == nil {
		return nil, fmt.Errorf("node %s not connected", nodeID)
	}
	if request == nil || request.Input == nil || request.TurnId == "" {
		return nil, fmt.Errorf("run request with input and turn_id is required")
	}
	if request.SessionId != "" {
		if err := p.ensureSessionOpen(a, request.SessionId); err != nil {
			return nil, err
		}
	}
	return p.dispatchMessage(nodeID, request.TurnId, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnRequest{RunTurnRequest: request}})
}

func (p *AgentPool) DispatchCommand(nodeID, taskID string, command *types.CommandRequest) (<-chan taskResult, error) {
	if command == nil || taskID == "" {
		return nil, fmt.Errorf("command and operation id are required")
	}
	a := p.get(nodeID)
	if a == nil {
		return nil, fmt.Errorf("node %s not connected", nodeID)
	}
	if command.SessionId != "" {
		if err := p.ensureSessionOpen(a, command.SessionId); err != nil {
			return nil, err
		}
	}
	return p.dispatchMessage(nodeID, taskID, &types.CommandProtocolMessage{Message: &types.CommandProtocolMessage_Request{Request: protobuf.Clone(command).(*types.CommandRequest)}})
}

func (p *AgentPool) dispatchMessage(nodeID, taskID string, message protobuf.Message) (<-chan taskResult, error) {
	a := p.get(nodeID)
	if a == nil {
		return nil, fmt.Errorf("node %s not connected", nodeID)
	}
	ch := make(chan taskResult, 1)
	st := a.state()
	st.mu.Lock()
	st.tasks[taskID] = ch
	st.turns[taskID] = 0
	st.mu.Unlock()
	envelope, err := aop.Wrap(taskID, "", message)
	if err != nil {
		st.dropTask(taskID)
		close(ch)
		return nil, err
	}
	if err := a.enqueue(envelope); err != nil {
		st.dropTask(taskID)
		close(ch)
		return nil, err
	}
	return ch, nil
}

// BroadcastConfigReload sends the committed protobuf config on the same FIFO as
// every other application message. Agents never fetch a second REST DTO.
func (p *AgentPool) BroadcastConfigReload(config *types.DistributeConfig) int {
	if config == nil {
		return 0
	}
	p.mu.RLock()
	agents := make([]*remoteAgent, 0, len(p.agents))
	for _, a := range p.agents {
		agents = append(agents, a)
	}
	p.mu.RUnlock()
	n := 0
	for _, a := range agents {
		a.reloadConfig(config)
		n++
	}
	return n
}

func (p *AgentPool) sendAgentMessage(nodeID, id, replyTo string, message protobuf.Message) error {
	a := p.get(nodeID)
	if a == nil {
		return fmt.Errorf("node %s not connected", nodeID)
	}
	envelope, err := aop.Wrap(id, replyTo, message)
	if err != nil {
		return err
	}
	return a.enqueue(envelope)
}

func (a *remoteAgent) enqueue(envelope *aop.Envelope) error {
	if a == nil || a.sendCh == nil {
		return fmt.Errorf("agent connection is unavailable")
	}
	select {
	case a.sendCh <- envelope:
		return nil
	case <-a.done:
		return fmt.Errorf("agent disconnected")
	}
}

func (p *AgentPool) CancelTask(nodeID, taskID string, sessionID ...string) error {
	a := p.get(nodeID)
	if a == nil {
		return nil
	}
	st := a.state()
	st.mu.Lock()
	_, isToolCall := st.toolCalls[taskID]
	st.mu.Unlock()
	resultCh, pending := st.dropTask(taskID)
	if !pending {
		return nil
	}
	var chatSessionID string
	if len(sessionID) > 0 {
		chatSessionID = sessionID[0]
	}
	requestID := generateID()
	cancelMessage := protobuf.Message(&aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelTurnRequest{CancelTurnRequest: &aop.CancelTurnRequest{
		SessionId: chatSessionID, TurnId: taskID,
	}}})
	if isToolCall {
		cancelMessage = &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelOperation{CancelOperation: &aop.CancelOperation{TargetId: taskID}}}
	}
	if resultCh != nil {
		close(resultCh)
	}
	return p.sendAgentMessage(nodeID, requestID, "", cancelMessage)
}

func (p *AgentPool) CancelPTY(nodeID, terminalID string) {
	_ = p.sendAgentMessage(nodeID, generateID(), "", terminalcodec.ToProto(pty.Frame{Type: pty.FrameKill, StreamID: terminalID}))
}

func (p *AgentPool) CloseTerminal(nodeID, terminalID string) {
	_ = p.sendAgentMessage(nodeID, generateID(), "", terminalcodec.ToProto(pty.Frame{Type: pty.FrameDetach, StreamID: terminalID}))
}

func (p *AgentPool) subscribePTY(nodeID, terminalID string) (<-chan pty.Frame, bool, func()) {
	ch := make(chan pty.Frame, 256)
	// Snapshot connectivity while registering the subscription under the pool
	// lock. An unregister cannot otherwise be distinguished from an initially
	// offline agent and can produce duplicate detached frames.
	p.mu.RLock()
	p.ptyMu.Lock()
	p.ptySubs[terminalID] = ch
	p.ptyNodeIDs[terminalID] = nodeID
	online := p.agents[nodeID] != nil
	p.ptyMu.Unlock()
	p.mu.RUnlock()
	return ch, online, func() {
		p.ptyMu.Lock()
		if p.ptySubs[terminalID] == ch {
			delete(p.ptySubs, terminalID)
			delete(p.ptyNodeIDs, terminalID)
			close(ch)
		}
		p.ptyMu.Unlock()
	}
}

func (p *AgentPool) notifyPTY(nodeID string, frame pty.Frame) {
	p.ptyMu.RLock()
	defer p.ptyMu.RUnlock()
	for terminalID, boundNodeID := range p.ptyNodeIDs {
		if boundNodeID != nodeID {
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
	for terminalID, nodeID := range p.ptyNodeIDs {
		if nodeID == agent.nodeID {
			terminalIDs = append(terminalIDs, terminalID)
		}
	}
	p.ptyMu.RUnlock()
	for _, terminalID := range terminalIDs {
		terminalID := terminalID
		go func() {
			_ = agent.enqueue(aop.MustWrap(generateID(), "", terminalcodec.ToProto(pty.Frame{Type: pty.FrameList, StreamID: terminalID})))
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

func aopToolResultText(content []*aop.Content) string {
	var parts []string
	for _, item := range content {
		if text := item.GetText().GetText(); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
