// Package pty routes canonical AOP PTY messages to the local PTY runtime.
package pty

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/aop"
	ptypb "github.com/chainreactors/cyber/aop/pty"
	procbus "github.com/chainreactors/cyber/core/proc"
	runtimeproc "github.com/chainreactors/utils/proc"
	"google.golang.org/protobuf/proto"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAttachBytes     = 64 * 1024
	DefaultMonitorInterval = 50 * time.Millisecond
)

type SendFunc func(*ptypb.ProtocolMessage)

type Router struct {
	mgr             runtimeproc.SessionManager
	openers         map[string]runtimeproc.OpenFunc
	attachBytes     int
	monitorInterval time.Duration

	mu       sync.Mutex
	sessions map[string]string
	cancels  map[string]context.CancelFunc
	resizers map[string]runtimeproc.ResizeFunc
}

type Option func(*Router)

func WithOpeners(openers map[string]runtimeproc.OpenFunc) Option {
	return func(r *Router) {
		for kind, opener := range openers {
			r.openers[kind] = opener
		}
	}
}

func WithOpener(kind string, opener runtimeproc.OpenFunc) Option {
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

func NewRouter(mgr runtimeproc.SessionManager, opts ...Option) *Router {
	r := &Router{
		mgr:             mgr,
		openers:         make(map[string]runtimeproc.OpenFunc),
		attachBytes:     DefaultAttachBytes,
		monitorInterval: DefaultMonitorInterval,
		sessions:        make(map[string]string),
		cancels:         make(map[string]context.CancelFunc),
		resizers:        make(map[string]runtimeproc.ResizeFunc),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// NewRuntimeRouter wraps the utils/pty runtime with the canonical AOP PTY
// protocol. Callers above this boundary only exchange ProtocolMessage values;
// runtime opener and session details remain private to this package.
func NewRuntimeRouter(mgr *runtimeproc.Manager, opts ...Option) *Router {
	defaults := []Option{WithOpeners(runtimeproc.DefaultOpeners(mgr, runtimeproc.DefaultSessionTimeout, runtimeproc.DefaultEnv()))}
	return NewRouter(mgr, append(defaults, opts...)...)
}

// Handler adapts the router to the AOP namespace contract. One handler serves
// one connection; every reply is addressed to the envelope that asked for it.
func (r *Router) Handler() aop.NamespaceHandler {
	return func(ctx context.Context, envelope *aop.Envelope, message proto.Message, send aop.SendFunc) error {
		value, ok := message.(*ptypb.ProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected PTY namespace message %T", message)
		}
		replyTo := envelope.GetId()
		r.handle(ctx, value, func(out *ptypb.ProtocolMessage) {
			_ = send(aop.Reply(replyTo, out))
		})
		return nil
	}
}

func (r *Router) handle(ctx context.Context, message *ptypb.ProtocolMessage, send SendFunc) {
	if send == nil {
		send = func(*ptypb.ProtocolMessage) {}
	}
	streamID := ptypb.StreamID(message)
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
	result, err := opener(ctx, runtimeproc.OpenSpec{
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
		StreamId: streamID, Session: procbus.SessionToProto(&info),
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

func (r *Router) attachExisting(ctx context.Context, streamID string, info runtimeproc.Info, cols, rows int, send SendFunc) {
	output, offset, err := r.mgr.SnapshotBytes(info.ID, r.attachBytes)
	if err != nil {
		r.sendError(send, streamID, err.Error())
		return
	}
	r.releaseStream(streamID)
	send(&ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Attached{Attached: &ptypb.Attached{
		StreamId: streamID, Session: procbus.SessionToProto(&info),
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
	send(ptypb.NewDetached(streamID))
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
	if info, ok := r.mgr.Get(sessionID); ok && info.State != runtimeproc.StateRunning {
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
			StreamId: streamID, Session: procbus.SessionToProto(&final),
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

func (r *Router) findReusableSession(kind, name string) (runtimeproc.Info, bool) {
	if r.mgr == nil {
		return runtimeproc.Info{}, false
	}
	var fallback runtimeproc.Info
	hasFallback := false
	for _, info := range r.mgr.List() {
		if info.State != runtimeproc.StateRunning || strings.ToLower(strings.TrimSpace(info.Kind)) != kind {
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

func newSessions(streamID string, sessions []runtimeproc.Info) *ptypb.ProtocolMessage {
	value := &ptypb.Sessions{StreamId: streamID, Sessions: make([]*ptypb.Session, 0, len(sessions))}
	for index := range sessions {
		value.Sessions = append(value.Sessions, procbus.SessionToProto(&sessions[index]))
	}
	return &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Sessions{Sessions: value}}
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
