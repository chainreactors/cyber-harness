// Package terminal routes canonical AOP PTY messages to the local PTY runtime.
package terminal

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	ptypb "github.com/chainreactors/aiscan/aop/pty"
	runtimepty "github.com/chainreactors/utils/pty"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	DefaultAttachBytes     = 64 * 1024
	DefaultMonitorInterval = 50 * time.Millisecond
)

type SendFunc func(*ptypb.ProtocolMessage)

type Router struct {
	mgr             runtimepty.SessionManager
	openers         map[string]runtimepty.OpenFunc
	attachBytes     int
	monitorInterval time.Duration

	mu       sync.Mutex
	sessions map[string]string
	cancels  map[string]context.CancelFunc
	resizers map[string]runtimepty.ResizeFunc
	streams  map[string]struct{}
}

type Option func(*Router)

func WithOpeners(openers map[string]runtimepty.OpenFunc) Option {
	return func(r *Router) {
		for kind, opener := range openers {
			r.openers[kind] = opener
		}
	}
}

func WithOpener(kind string, opener runtimepty.OpenFunc) Option {
	return func(r *Router) {
		if kind != "" && opener != nil {
			r.openers[strings.ToLower(strings.TrimSpace(kind))] = opener
		}
	}
}

func WithAttachBytes(n int) Option {
	return func(r *Router) {
		if n > 0 {
			r.attachBytes = n
		}
	}
}

func WithMonitorInterval(interval time.Duration) Option {
	return func(r *Router) {
		if interval > 0 {
			r.monitorInterval = interval
		}
	}
}

func NewRouter(mgr runtimepty.SessionManager, opts ...Option) *Router {
	r := &Router{
		mgr:             mgr,
		openers:         make(map[string]runtimepty.OpenFunc),
		attachBytes:     DefaultAttachBytes,
		monitorInterval: DefaultMonitorInterval,
		sessions:        make(map[string]string),
		cancels:         make(map[string]context.CancelFunc),
		resizers:        make(map[string]runtimepty.ResizeFunc),
		streams:         make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// NewRuntimeRouter wraps the utils/pty runtime with the canonical AOP PTY
// protocol. Callers above this boundary only exchange ProtocolMessage values;
// runtime opener and session details remain private to this package.
func NewRuntimeRouter(mgr *runtimepty.Manager, opts ...Option) *Router {
	defaults := []Option{WithOpeners(runtimepty.DefaultOpeners(mgr, runtimepty.DefaultSessionTimeout, runtimepty.DefaultEnv()))}
	return NewRouter(mgr, append(defaults, opts...)...)
}

func (r *Router) Handle(ctx context.Context, message *ptypb.ProtocolMessage, send SendFunc) {
	if send == nil {
		send = func(*ptypb.ProtocolMessage) {}
	}
	streamID := StreamID(message)
	r.touchStream(streamID)
	defer func() {
		if value := recover(); value != nil {
			r.sendError(send, streamID, fmt.Sprintf("panic: %v", value))
		}
	}()
	if message == nil {
		r.sendError(send, streamID, "empty pty message")
		return
	}
	switch payload := message.Message.(type) {
	case *ptypb.ProtocolMessage_Open:
		r.open(ctx, payload.Open, send)
	case *ptypb.ProtocolMessage_Attach:
		r.attach(ctx, payload.Attach, send)
	case *ptypb.ProtocolMessage_Detach:
		r.detach(payload.Detach.GetStreamId(), send)
	case *ptypb.ProtocolMessage_List:
		r.list(payload.List.GetStreamId(), send)
	case *ptypb.ProtocolMessage_Input:
		r.input(payload.Input, send)
	case *ptypb.ProtocolMessage_Resize:
		r.resize(payload.Resize, send)
	case *ptypb.ProtocolMessage_Kill:
		r.kill(payload.Kill.GetStreamId(), send)
	case *ptypb.ProtocolMessage_Close:
		r.kill(payload.Close.GetStreamId(), send)
	default:
		r.sendError(send, streamID, "unsupported pty message")
	}
}

func (r *Router) open(ctx context.Context, request *ptypb.Open, send SendFunc) {
	if request == nil {
		r.sendError(send, "", "pty open request required")
		return
	}
	streamID := request.GetStreamId()
	if r.mgr == nil {
		r.sendError(send, streamID, "pty manager unavailable")
		return
	}
	if streamID == "" {
		r.sendError(send, streamID, "pty stream_id required")
		return
	}
	kind := normalizeKind(request.GetKind(), request.GetCommand())
	name := request.GetName()
	if name == "" {
		name = defaultName(kind)
	}
	if request.GetSingleton() {
		if info, ok := r.findReusableSession(kind, name); ok {
			r.attachExisting(ctx, streamID, info, int(request.GetCols()), int(request.GetRows()), send)
			return
		}
	}
	opener := r.openers[kind]
	if opener == nil {
		r.sendError(send, streamID, "unsupported pty kind: "+kind)
		return
	}
	result, err := opener(ctx, runtimepty.OpenSpec{
		Kind: kind, Name: name, Command: request.GetCommand(), Args: append([]string(nil), request.GetArgs()...),
		Cols: int(request.GetCols()), Rows: int(request.GetRows()),
	})
	if err != nil {
		r.sendError(send, streamID, err.Error())
		return
	}
	info := result.Info
	r.releaseStream(streamID)
	if result.Resize != nil {
		r.mu.Lock()
		r.resizers[info.ID] = result.Resize
		r.mu.Unlock()
	}
	send(&ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Opened{Opened: &ptypb.Opened{
		StreamId: streamID, Session: sessionToProto(&info),
	}}})
	r.monitor(ctx, streamID, info.ID, 0, send)
	r.resizeSession(streamID, info.ID, int(request.GetCols()), int(request.GetRows()), send)
}

func (r *Router) attach(ctx context.Context, request *ptypb.Attach, send SendFunc) {
	if request == nil {
		r.sendError(send, "", "pty attach request required")
		return
	}
	streamID := request.GetStreamId()
	if r.mgr == nil {
		r.sendError(send, streamID, "pty manager unavailable")
		return
	}
	if streamID == "" {
		r.sendError(send, streamID, "pty stream_id required")
		return
	}
	if request.GetSessionId() == "" {
		r.sendError(send, streamID, "pty session_id required")
		return
	}
	info, ok := r.mgr.Get(request.GetSessionId())
	if !ok {
		r.sendError(send, streamID, "no such session: "+request.GetSessionId())
		return
	}
	r.attachExisting(ctx, streamID, info, int(request.GetCols()), int(request.GetRows()), send)
}

func (r *Router) attachExisting(ctx context.Context, streamID string, info runtimepty.Info, cols, rows int, send SendFunc) {
	output, offset, err := r.mgr.SnapshotBytes(info.ID, r.attachBytes)
	if err != nil {
		r.sendError(send, streamID, err.Error())
		return
	}
	r.releaseStream(streamID)
	send(&ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Attached{Attached: &ptypb.Attached{
		StreamId: streamID, Session: sessionToProto(&info),
	}}})
	if len(output) > 0 {
		send(&ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Output{Output: &ptypb.Output{
			StreamId: streamID, Data: output,
		}}})
	}
	r.monitor(ctx, streamID, info.ID, offset, send)
	r.resizeSession(streamID, info.ID, cols, rows, send)
}

func (r *Router) detach(streamID string, send SendFunc) {
	r.releaseStream(streamID)
	r.dropStream(streamID)
	send(NewDetached(streamID))
}

func (r *Router) list(streamID string, send SendFunc) {
	if r.mgr == nil {
		r.sendError(send, streamID, "pty manager unavailable")
		return
	}
	send(newSessions(streamID, r.mgr.List()))
}

func (r *Router) input(request *ptypb.Input, send SendFunc) {
	if request == nil || r.mgr == nil {
		return
	}
	streamID := request.GetStreamId()
	sessionID := r.sessionForStream(streamID)
	if sessionID == "" {
		r.sendError(send, streamID, "pty session_id required")
		return
	}
	if info, ok := r.mgr.Get(sessionID); ok && info.State != runtimepty.StateRunning {
		return
	}
	if err := r.mgr.Write(sessionID, request.GetData()); err != nil {
		r.sendError(send, streamID, err.Error())
	}
}

func (r *Router) resize(request *ptypb.Resize, send SendFunc) {
	if request == nil || r.mgr == nil {
		return
	}
	streamID := request.GetStreamId()
	sessionID := r.sessionForStream(streamID)
	if sessionID == "" {
		return
	}
	r.resizeSession(streamID, sessionID, int(request.GetCols()), int(request.GetRows()), send)
}

func (r *Router) resizeSession(streamID, sessionID string, cols, rows int, send SendFunc) {
	if cols <= 0 || rows <= 0 {
		return
	}
	r.mu.Lock()
	resize := r.resizers[sessionID]
	r.mu.Unlock()
	if resize != nil {
		resize(cols, rows)
	}
	if err := r.mgr.Resize(sessionID, cols, rows); err != nil {
		r.sendError(send, streamID, err.Error())
	}
}

func (r *Router) kill(streamID string, send SendFunc) {
	if r.mgr == nil {
		return
	}
	sessionID := r.sessionForStream(streamID)
	if sessionID == "" {
		return
	}
	if err := r.mgr.Kill(sessionID); err != nil {
		r.sendError(send, streamID, err.Error())
	}
}

func (r *Router) Close() {
	r.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(r.cancels))
	for _, cancel := range r.cancels {
		cancels = append(cancels, cancel)
	}
	r.sessions = make(map[string]string)
	r.cancels = make(map[string]context.CancelFunc)
	r.resizers = make(map[string]runtimepty.ResizeFunc)
	r.streams = make(map[string]struct{})
	r.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (r *Router) monitor(ctx context.Context, streamID, sessionID string, offset int64, send SendFunc) {
	if r.mgr == nil {
		return
	}
	monitorCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if old := r.cancels[streamID]; old != nil {
		old()
	}
	r.sessions[streamID] = sessionID
	r.cancels[streamID] = cancel
	r.mu.Unlock()

	err := r.mgr.MonitorFrom(monitorCtx, sessionID, offset, r.monitorInterval, func(output []byte) {
		if r.sessionForStream(streamID) == sessionID {
			send(&ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Output{Output: &ptypb.Output{
				StreamId: streamID, Data: output,
			}}})
		}
	})
	if err != nil {
		cancel()
		r.releaseStream(streamID)
		r.sendError(send, streamID, err.Error())
		return
	}

	go func() {
		final, err := r.mgr.Wait(monitorCtx, sessionID, 0)
		if err != nil {
			return
		}
		r.mu.Lock()
		if r.sessions[streamID] != sessionID {
			r.mu.Unlock()
			return
		}
		delete(r.sessions, streamID)
		delete(r.cancels, streamID)
		delete(r.resizers, sessionID)
		r.mu.Unlock()
		send(&ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Closed{Closed: &ptypb.Closed{
			StreamId: streamID, Session: sessionToProto(&final),
		}}})
	}()
}

func (r *Router) releaseStream(streamID string) string {
	if streamID == "" {
		return ""
	}
	r.mu.Lock()
	sessionID := r.sessions[streamID]
	cancel := r.cancels[streamID]
	delete(r.sessions, streamID)
	delete(r.cancels, streamID)
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return sessionID
}

func (r *Router) sessionForStream(streamID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[streamID]
}

func (r *Router) StreamIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	streamIDs := make([]string, 0, len(r.streams))
	for streamID := range r.streams {
		streamIDs = append(streamIDs, streamID)
	}
	return streamIDs
}

// BroadcastSessions emits the current runtime session state as canonical AOP
// PTY messages for every active stream.
func (r *Router) BroadcastSessions(send SendFunc) {
	if r == nil || r.mgr == nil || send == nil {
		return
	}
	sessions := r.mgr.List()
	for _, streamID := range r.StreamIDs() {
		send(newSessions(streamID, sessions))
	}
}

func (r *Router) touchStream(streamID string) {
	if streamID == "" {
		return
	}
	r.mu.Lock()
	r.streams[streamID] = struct{}{}
	r.mu.Unlock()
}

func (r *Router) dropStream(streamID string) {
	if streamID == "" {
		return
	}
	r.mu.Lock()
	delete(r.streams, streamID)
	r.mu.Unlock()
}

func (r *Router) findReusableSession(kind, name string) (runtimepty.Info, bool) {
	if r.mgr == nil {
		return runtimepty.Info{}, false
	}
	var fallback runtimepty.Info
	hasFallback := false
	for _, info := range r.mgr.List() {
		if info.State != runtimepty.StateRunning || strings.ToLower(strings.TrimSpace(info.Kind)) != kind {
			continue
		}
		if name != "" && info.Name == name {
			return info, true
		}
		if !hasFallback {
			fallback = info
			hasFallback = true
		}
	}
	return fallback, hasFallback
}

func (r *Router) sendError(send SendFunc, streamID, message string) {
	send(&ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Error{Error: &ptypb.Error{
		StreamId: streamID, Message: message,
	}}})
}

func StreamID(message *ptypb.ProtocolMessage) string {
	if message == nil {
		return ""
	}
	switch payload := message.Message.(type) {
	case *ptypb.ProtocolMessage_Open:
		return payload.Open.GetStreamId()
	case *ptypb.ProtocolMessage_Opened:
		return payload.Opened.GetStreamId()
	case *ptypb.ProtocolMessage_Input:
		return payload.Input.GetStreamId()
	case *ptypb.ProtocolMessage_Output:
		return payload.Output.GetStreamId()
	case *ptypb.ProtocolMessage_Resize:
		return payload.Resize.GetStreamId()
	case *ptypb.ProtocolMessage_List:
		return payload.List.GetStreamId()
	case *ptypb.ProtocolMessage_Sessions:
		return payload.Sessions.GetStreamId()
	case *ptypb.ProtocolMessage_Attach:
		return payload.Attach.GetStreamId()
	case *ptypb.ProtocolMessage_Attached:
		return payload.Attached.GetStreamId()
	case *ptypb.ProtocolMessage_Detach:
		return payload.Detach.GetStreamId()
	case *ptypb.ProtocolMessage_Detached:
		return payload.Detached.GetStreamId()
	case *ptypb.ProtocolMessage_Kill:
		return payload.Kill.GetStreamId()
	case *ptypb.ProtocolMessage_Close:
		return payload.Close.GetStreamId()
	case *ptypb.ProtocolMessage_Closed:
		return payload.Closed.GetStreamId()
	case *ptypb.ProtocolMessage_State:
		return payload.State.GetStreamId()
	case *ptypb.ProtocolMessage_Error:
		return payload.Error.GetStreamId()
	default:
		return ""
	}
}

func NodeID(message *ptypb.ProtocolMessage) string {
	if message == nil {
		return ""
	}
	switch payload := message.Message.(type) {
	case *ptypb.ProtocolMessage_Open:
		return payload.Open.GetNodeId()
	case *ptypb.ProtocolMessage_List:
		return payload.List.GetNodeId()
	default:
		return ""
	}
}

func IsDetach(message *ptypb.ProtocolMessage) bool {
	_, ok := message.GetMessage().(*ptypb.ProtocolMessage_Detach)
	return ok
}

func IsClosed(message *ptypb.ProtocolMessage) bool {
	_, ok := message.GetMessage().(*ptypb.ProtocolMessage_Closed)
	return ok
}

func NewList(streamID, nodeID string) *ptypb.ProtocolMessage {
	return &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_List{List: &ptypb.List{StreamId: streamID, NodeId: nodeID}}}
}

func NewKill(streamID string) *ptypb.ProtocolMessage {
	return &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Kill{Kill: &ptypb.Kill{StreamId: streamID}}}
}

func NewDetach(streamID string) *ptypb.ProtocolMessage {
	return &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Detach{Detach: &ptypb.Detach{StreamId: streamID}}}
}

func NewDetached(streamID string) *ptypb.ProtocolMessage {
	return &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Detached{Detached: &ptypb.Detached{StreamId: streamID}}}
}

func newSessions(streamID string, sessions []runtimepty.Info) *ptypb.ProtocolMessage {
	value := &ptypb.Sessions{StreamId: streamID, Sessions: make([]*ptypb.Session, 0, len(sessions))}
	for index := range sessions {
		value.Sessions = append(value.Sessions, sessionToProto(&sessions[index]))
	}
	return &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Sessions{Sessions: value}}
}

func sessionToProto(value *runtimepty.Info) *ptypb.Session {
	if value == nil {
		return nil
	}
	info := &ptypb.Session{
		Id: value.ID, Kind: value.Kind, Name: value.Name, Command: value.Command,
		Pid: int32(value.PID), ActivitySeq: value.ActivitySeq, OutputBytes: value.OutputBytes,
		ExitCode: int32(value.ExitCode), State: string(value.State), KillCause: value.KillCause,
	}
	if !value.StartedAt.IsZero() {
		info.StartedAt = timestamppb.New(value.StartedAt)
	}
	if !value.LastActivityAt.IsZero() {
		info.LastActivityAt = timestamppb.New(value.LastActivityAt)
	}
	if !value.EndedAt.IsZero() {
		info.EndedAt = timestamppb.New(value.EndedAt)
	}
	return info
}

func normalizeKind(kind, command string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		if strings.TrimSpace(command) != "" {
			return "command"
		}
		return "shell"
	}
	return kind
}

func defaultName(kind string) string {
	switch kind {
	case "repl":
		return "remote-repl"
	case "command":
		return "remote-command"
	default:
		return "remote-shell"
	}
}
