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
	agentpb "github.com/chainreactors/aiscan/pkg/types/agent"
	commandpb "github.com/chainreactors/aiscan/pkg/types/command"
	configpb "github.com/chainreactors/aiscan/pkg/types/config"
	reloadpb "github.com/chainreactors/aiscan/pkg/types/reload"
	terminalcodec "github.com/chainreactors/aiscan/pkg/web/terminal"
	"github.com/chainreactors/ioa/protocols"
	"github.com/chainreactors/utils/pty"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func cloneCommandSpecs(values []*commandpb.Spec) []*commandpb.Spec {
	if len(values) == 0 {
		return nil
	}
	out := make([]*commandpb.Spec, 0, len(values))
	for _, value := range values {
		if value != nil {
			out = append(out, protobuf.Clone(value).(*commandpb.Spec))
		}
	}
	return out
}

type taskResult struct {
	Output string
	Result json.RawMessage
	File   *filepb.Result
	Err    string
	Turn   int
}

type remoteAgent struct {
	nodeURI      string
	name         string
	capabilities []string
	commandsMenu []*commandpb.Spec
	close        func()
	sendCh       chan *aop.Envelope
	connectAt    time.Time
	node         protocols.NodeRef
	runtime      *aop.AgentRuntimeInfo
	status       *aop.AgentStatus
	stats        *aop.AgentStats

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

func (a *remoteAgent) view() *agentpb.View {
	a.mu.Lock()
	defer a.mu.Unlock()
	hello := &aop.AgentHello{
		AgentId:      a.node.ID,
		Name:         a.name,
		Authority:    a.node.Authority,
		Capabilities: append([]string(nil), a.capabilities...),
	}
	if a.runtime != nil {
		hello.Runtime = protobuf.Clone(a.runtime).(*aop.AgentRuntimeInfo)
	}
	view := &agentpb.View{Hello: hello, NodeUri: a.node.URI(), ConnectedAt: timestamppb.New(a.connectAt), Commands: cloneCommandSpecs(a.commandsMenu), Busy: len(a.tasks) > 0}
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
func (a *remoteAgent) commandSpecs() []*commandpb.Spec {
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

// AgentPool manages connected remote aiscan agents via WebSocket.
type AgentPool struct {
	mu             sync.RWMutex
	agents         map[string]*remoteAgent
	hub            *Hub
	sessions       SessionLookup
	sco            SCOStore
	config         func(context.Context) (*configpb.DistributeConfig, error)
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

func (p *AgentPool) SetSCOStore(store SCOStore) {
	p.sco = store
}

// agentKey is the pool key for a registering agent: its canonical Web identity, so
// a reconnecting agent (WS flap, hub restart, config-driven bounce) re-registers
// under the SAME key. The hub used to mint a throwaway id per connection, which
// dangled every chat session bound to it — the session freezes the node URI at
// creation, so on reconnect the stored URI resolved to nothing and the chat
// rejected every message as "not connected" even with the agent right back.
func agentKey(agentID, authority string) string {
	return (protocols.NodeRef{ID: agentID, Authority: authority}).URI()
}

func (p *AgentPool) register(a *remoteAgent) {
	p.mu.Lock()
	old := p.agents[a.nodeURI]
	p.agents[a.nodeURI] = a
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
	removed := p.agents[a.nodeURI] == a
	if removed {
		delete(p.agents, a.nodeURI)
	}
	p.mu.Unlock()
	if removed {
		p.notifyPTY(a.nodeURI, pty.Frame{Type: pty.FrameDetached})
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

func (p *AgentPool) get(nodeURI string) *remoteAgent {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.agents[nodeURI]
}

func (p *AgentPool) List() []*agentpb.View {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*agentpb.View, 0, len(p.agents))
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
func (p *AgentPool) DispatchToolCall(nodeURI, taskID string, call *aop.ToolCall) (<-chan taskResult, error) {
	a := p.get(nodeURI)
	if a == nil {
		return nil, fmt.Errorf("node %s not connected", nodeURI)
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
		agentName = a.nodeURI
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
	ch, err := p.dispatchMessage(nodeURI, taskID, &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{Call: &toolpb.Call{
		SessionId: sessionID, TurnId: taskID, Call: call,
	}}})
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
func (p *AgentPool) DispatchChat(nodeURI, taskID, prompt string) (<-chan taskResult, error) {
	return p.DispatchRun(nodeURI, &aop.RunTurnRequest{
		TurnId: taskID,
		Input:  &aop.Message{Role: "user", Content: []*aop.Content{{Value: &aop.Content_Text{Text: &aop.TextContent{Text: prompt}}}}},
	})
}

func (p *AgentPool) DispatchOpenSession(nodeURI, requestID string, request *aop.OpenSessionRequest) (<-chan taskResult, error) {
	if request == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(request.SessionId) == "" {
		return nil, fmt.Errorf("open session envelope id and session_id are required")
	}
	return p.dispatchMessage(nodeURI, requestID, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{OpenSessionRequest: request}})
}

func (p *AgentPool) SessionOpen(nodeURI, sessionID string) bool {
	agent := p.get(nodeURI)
	if agent == nil {
		return false
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	_, ok := agent.openSessions[sessionID]
	return ok
}

func (p *AgentPool) DispatchCloseSession(nodeURI, requestID string, request *aop.CloseSessionRequest) (<-chan taskResult, error) {
	if request == nil || strings.TrimSpace(requestID) == "" || strings.TrimSpace(request.SessionId) == "" {
		return nil, fmt.Errorf("close session envelope id and session_id are required")
	}
	return p.dispatchMessage(nodeURI, requestID, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionRequest{CloseSessionRequest: request}})
}

func (p *AgentPool) DispatchRun(nodeURI string, request *aop.RunTurnRequest) (<-chan taskResult, error) {
	a := p.get(nodeURI)
	if a == nil {
		return nil, fmt.Errorf("node %s not connected", nodeURI)
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
			requestID := "open:" + request.SessionId
			if err := p.sendAgentMessage(nodeURI, requestID, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{OpenSessionRequest: &aop.OpenSessionRequest{
				SessionId: request.SessionId, NodeUri: nodeURI,
			}}}); err != nil {
				a.mu.Lock()
				delete(a.openSessions, request.SessionId)
				a.mu.Unlock()
				return nil, err
			}
		}
	}
	return p.dispatchMessage(nodeURI, request.TurnId, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnRequest{RunTurnRequest: request}})
}

func (p *AgentPool) DispatchCommand(nodeURI, taskID string, command *commandpb.Request) (<-chan taskResult, error) {
	if command == nil || taskID == "" {
		return nil, fmt.Errorf("command and operation id are required")
	}
	a := p.get(nodeURI)
	if a == nil {
		return nil, fmt.Errorf("node %s not connected", nodeURI)
	}
	if command.SessionId != "" {
		a.mu.Lock()
		_, opened := a.openSessions[command.SessionId]
		if !opened {
			a.openSessions[command.SessionId] = struct{}{}
		}
		a.mu.Unlock()
		if !opened {
			requestID := "open:" + command.SessionId
			if err := p.sendAgentMessage(nodeURI, requestID, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{OpenSessionRequest: &aop.OpenSessionRequest{
				SessionId: command.SessionId, NodeUri: nodeURI,
			}}}); err != nil {
				a.mu.Lock()
				delete(a.openSessions, command.SessionId)
				a.mu.Unlock()
				return nil, err
			}
		}
	}
	return p.dispatchMessage(nodeURI, taskID, &commandpb.ProtocolMessage{Message: &commandpb.ProtocolMessage_Request{Request: protobuf.Clone(command).(*commandpb.Request)}})
}

func (p *AgentPool) dispatchMessage(nodeURI, taskID string, message protobuf.Message) (<-chan taskResult, error) {
	a := p.get(nodeURI)
	if a == nil {
		return nil, fmt.Errorf("node %s not connected", nodeURI)
	}
	ch := make(chan taskResult, 1)
	a.mu.Lock()
	a.tasks[taskID] = ch
	a.turns[taskID] = 0
	a.mu.Unlock()
	envelope, err := aop.Wrap(taskID, "", message)
	if err != nil {
		a.mu.Lock()
		delete(a.tasks, taskID)
		delete(a.turns, taskID)
		a.mu.Unlock()
		close(ch)
		return nil, err
	}
	if err := a.enqueue(envelope); err != nil {
		a.mu.Lock()
		delete(a.tasks, taskID)
		delete(a.turns, taskID)
		a.mu.Unlock()
		close(ch)
		return nil, err
	}
	return ch, nil
}

// BroadcastConfigReload sends the committed protobuf config on the same FIFO as
// every other application message. Agents never fetch a second REST DTO.
func (p *AgentPool) BroadcastConfigReload(config *configpb.DistributeConfig) int {
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
		message := &reloadpb.ProtocolMessage{Message: &reloadpb.ProtocolMessage_Request{Request: &reloadpb.Request{Config: protobuf.Clone(config).(*configpb.DistributeConfig)}}}
		envelope, err := aop.Wrap(generateID(), "", message)
		if err == nil && a.enqueue(envelope) == nil {
			n++
		}
	}
	return n
}

func (p *AgentPool) sendAgentMessage(nodeURI, id, replyTo string, message protobuf.Message) error {
	a := p.get(nodeURI)
	if a == nil {
		return fmt.Errorf("node %s not connected", nodeURI)
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

func (p *AgentPool) CancelTask(nodeURI, taskID string, sessionID ...string) error {
	a := p.get(nodeURI)
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
	return p.sendAgentMessage(nodeURI, requestID, "", cancelMessage)
}

func (p *AgentPool) CancelPTY(nodeURI, terminalID string) {
	_ = p.sendAgentMessage(nodeURI, generateID(), "", terminalcodec.ToProto(pty.Frame{Type: pty.FrameKill, StreamID: terminalID}))
}

func (p *AgentPool) CloseTerminal(nodeURI, terminalID string) {
	_ = p.sendAgentMessage(nodeURI, generateID(), "", terminalcodec.ToProto(pty.Frame{Type: pty.FrameDetach, StreamID: terminalID}))
}

func (p *AgentPool) subscribePTY(nodeURI, terminalID string) (<-chan pty.Frame, bool, func()) {
	ch := make(chan pty.Frame, 256)
	// Snapshot connectivity while registering the subscription under the pool
	// lock. An unregister cannot otherwise be distinguished from an initially
	// offline agent and can produce duplicate detached frames.
	p.mu.RLock()
	p.ptyMu.Lock()
	p.ptySubs[terminalID] = ch
	p.ptyAgents[terminalID] = nodeURI
	online := p.agents[nodeURI] != nil
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

func (p *AgentPool) notifyPTY(nodeURI string, frame pty.Frame) {
	p.ptyMu.RLock()
	defer p.ptyMu.RUnlock()
	for terminalID, boundNodeURI := range p.ptyAgents {
		if boundNodeURI != nodeURI {
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
	for terminalID, nodeURI := range p.ptyAgents {
		if nodeURI == agent.nodeURI {
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

// convergeTaskOnToolResult closes a tool.call task on its terminal
// tool.result. SCO facts travel independently through the SCO namespace;
// tool.result carries only operation completion and human-readable output.
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
	ch <- res
	close(ch)
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
		}
	}
	return strings.Join(parts, "\n")
}
