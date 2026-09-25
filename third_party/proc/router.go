package proc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAttachBytes     = 64 * 1024
	DefaultMonitorInterval = 50 * time.Millisecond
)

type Router struct {
	mgr             SessionManager
	openers         map[string]OpenFunc
	attachBytes     int
	monitorInterval time.Duration

	mu       sync.Mutex
	sessions map[string]string
	cancels  map[string]context.CancelFunc
	resizers map[string]ResizeFunc
	streams  map[string]struct{}
}

type Option func(*Router)

func WithOpeners(openers map[string]OpenFunc) Option {
	return func(r *Router) {
		for kind, opener := range openers {
			r.openers[kind] = opener
		}
	}
}

func WithOpener(kind string, opener OpenFunc) Option {
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

func NewRouter(mgr SessionManager, opts ...Option) *Router {
	r := &Router{
		mgr:             mgr,
		openers:         make(map[string]OpenFunc),
		attachBytes:     DefaultAttachBytes,
		monitorInterval: DefaultMonitorInterval,
		sessions:        make(map[string]string),
		cancels:         make(map[string]context.CancelFunc),
		resizers:        make(map[string]ResizeFunc),
		streams:         make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Router) Serve(ctx context.Context, conn io.ReadWriteCloser) error {
	if conn == nil {
		return fmt.Errorf("remotepty conn is nil")
	}
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)
	var mu sync.Mutex

	for {
		var frame Frame
		err := decoder.Decode(&frame)
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				r.Close()
				return nil
			}
			r.Close()
			return err
		}
		r.Handle(ctx, frame, func(out Frame) {
			mu.Lock()
			defer mu.Unlock()
			_ = encoder.Encode(out)
		})
	}
}

func (r *Router) Handle(ctx context.Context, frame Frame, send func(Frame)) {
	if send == nil {
		send = func(Frame) {}
	}
	r.touchStream(frame.StreamID)
	defer func() {
		if v := recover(); v != nil {
			r.sendError(send, frame.StreamID, fmt.Sprintf("panic: %v", v))
		}
	}()
	switch frame.Type {
	case FrameOpen:
		r.open(ctx, frame, send)
	case FrameAttach:
		r.attach(ctx, frame, send)
	case FrameDetach:
		r.detach(frame, send)
	case FrameList:
		r.list(frame.StreamID, send)
	case FrameInput:
		r.input(frame, send)
	case FrameResize:
		r.resize(frame, send)
	case FrameKill:
		r.kill(frame, send)
	default:
		r.sendError(send, frame.StreamID, "unsupported pty frame: "+string(frame.Type))
	}
}

func (r *Router) open(ctx context.Context, frame Frame, send func(Frame)) {
	if r.mgr == nil {
		r.sendError(send, frame.StreamID, "pty manager unavailable")
		return
	}
	if frame.StreamID == "" {
		r.sendError(send, frame.StreamID, "pty stream_id required")
		return
	}
	kind := normalizeKind(frame.Kind, frame.Command)
	name := frame.Name
	if name == "" {
		name = defaultName(kind)
	}
	if frame.Singleton {
		if info, ok := r.findReusableSession(kind, name); ok {
			frame.SessionID = info.ID
			r.attachExisting(ctx, frame, info, send)
			return
		}
	}
	opener := r.openers[kind]
	if opener == nil {
		r.sendError(send, frame.StreamID, "unsupported pty kind: "+kind)
		return
	}

	result, err := opener(ctx, OpenSpec{
		Kind:    kind,
		Name:    name,
		Command: frame.Command,
		Args:    append([]string(nil), frame.Args...),
		Cols:    frame.Cols,
		Rows:    frame.Rows,
	})
	if err != nil {
		r.sendError(send, frame.StreamID, err.Error())
		return
	}

	info := result.Info
	r.releaseStream(frame.StreamID, false)
	if result.Resize != nil {
		r.mu.Lock()
		r.resizers[info.ID] = result.Resize
		r.mu.Unlock()
	}
	send(Frame{
		Type:      FrameOpened,
		StreamID:  frame.StreamID,
		SessionID: info.ID,
		Kind:      kind,
		Name:      info.Name,
		Session:   &info,
	})
	r.monitor(ctx, frame.StreamID, info.ID, 0, send)
	r.resizeSession(frame.StreamID, info.ID, frame.Cols, frame.Rows, send)
}

func (r *Router) attach(ctx context.Context, frame Frame, send func(Frame)) {
	if r.mgr == nil {
		r.sendError(send, frame.StreamID, "pty manager unavailable")
		return
	}
	if frame.StreamID == "" {
		r.sendError(send, frame.StreamID, "pty stream_id required")
		return
	}
	if frame.SessionID == "" {
		r.sendError(send, frame.StreamID, "pty session_id required")
		return
	}
	info, ok := r.mgr.Get(frame.SessionID)
	if !ok {
		r.sendError(send, frame.StreamID, "no such session: "+frame.SessionID)
		return
	}
	r.attachExisting(ctx, frame, info, send)
}

func (r *Router) attachExisting(ctx context.Context, frame Frame, info Info, send func(Frame)) {
	n := frame.Bytes
	if n <= 0 {
		n = r.attachBytes
	}
	output, offset, err := r.mgr.SnapshotBytes(info.ID, n)
	if err != nil {
		r.sendError(send, frame.StreamID, err.Error())
		return
	}

	r.releaseStream(frame.StreamID, false)
	send(Frame{
		Type:      FrameAttached,
		StreamID:  frame.StreamID,
		SessionID: info.ID,
		Kind:      info.Kind,
		Name:      info.Name,
		Session:   &info,
	})
	if len(output) > 0 {
		send(Frame{Type: FrameOutput, StreamID: frame.StreamID, SessionID: info.ID, Data: output})
	}
	r.monitor(ctx, frame.StreamID, info.ID, offset, send)
	r.resizeSession(frame.StreamID, info.ID, frame.Cols, frame.Rows, send)
}

func (r *Router) detach(frame Frame, send func(Frame)) {
	sessionID := r.releaseStream(frame.StreamID, false)
	r.dropStream(frame.StreamID)
	send(Frame{Type: FrameDetached, StreamID: frame.StreamID, SessionID: sessionID})
}

func (r *Router) list(streamID string, send func(Frame)) {
	if r.mgr == nil {
		r.sendError(send, streamID, "pty manager unavailable")
		return
	}
	send(Frame{Type: FrameSessions, StreamID: streamID, Sessions: r.mgr.List()})
}

func (r *Router) input(frame Frame, send func(Frame)) {
	if r.mgr == nil {
		r.sendError(send, frame.StreamID, "pty manager unavailable")
		return
	}
	sessionID := frame.SessionID
	if sessionID == "" {
		sessionID = r.sessionForStream(frame.StreamID)
	}
	if sessionID == "" {
		r.sendError(send, frame.StreamID, "pty session_id required")
		return
	}
	if info, ok := r.mgr.Get(sessionID); ok && info.State != StateRunning {
		return
	}
	if err := r.mgr.Write(sessionID, frame.Data); err != nil {
		r.sendError(send, frame.StreamID, err.Error())
	}
}

func (r *Router) resize(frame Frame, send func(Frame)) {
	if r.mgr == nil {
		r.sendError(send, frame.StreamID, "pty manager unavailable")
		return
	}
	sessionID := frame.SessionID
	if sessionID == "" {
		sessionID = r.sessionForStream(frame.StreamID)
	}
	if sessionID == "" {
		return
	}
	r.resizeSession(frame.StreamID, sessionID, frame.Cols, frame.Rows, send)
}

func (r *Router) resizeSession(streamID, sessionID string, cols, rows int, send func(Frame)) {
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

func (r *Router) kill(frame Frame, send func(Frame)) {
	if r.mgr == nil {
		return
	}
	sessionID := frame.SessionID
	if sessionID == "" {
		sessionID = r.sessionForStream(frame.StreamID)
	}
	if sessionID == "" {
		return
	}
	if err := r.mgr.Kill(sessionID); err != nil {
		r.sendError(send, frame.StreamID, err.Error())
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
	r.resizers = make(map[string]ResizeFunc)
	r.streams = make(map[string]struct{})
	r.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (r *Router) monitor(ctx context.Context, streamID, sessionID string, offset int64, send func(Frame)) {
	if r.mgr == nil {
		return
	}
	monCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if old := r.cancels[streamID]; old != nil {
		old()
	}
	r.sessions[streamID] = sessionID
	r.cancels[streamID] = cancel
	r.mu.Unlock()

	err := r.mgr.MonitorFrom(monCtx, sessionID, offset, r.monitorInterval, func(output []byte) {
		if r.sessionForStream(streamID) == sessionID {
			send(Frame{Type: FrameOutput, StreamID: streamID, SessionID: sessionID, Data: output})
		}
	})
	if err != nil {
		cancel()
		r.releaseStream(streamID, false)
		r.sendError(send, streamID, err.Error())
		return
	}

	go func() {
		final, err := r.mgr.Wait(monCtx, sessionID, 0)
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
		send(Frame{
			Type:      FrameClosed,
			StreamID:  streamID,
			SessionID: sessionID,
			State:     final.State,
			ExitCode:  final.ExitStatus(),
			Session:   &final,
		})
	}()
}

func (r *Router) releaseStream(streamID string, kill bool) string {
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
	if kill && r.mgr != nil && sessionID != "" {
		_ = r.mgr.Kill(sessionID)
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

func (r *Router) findReusableSession(kind, name string) (Info, bool) {
	if r.mgr == nil {
		return Info{}, false
	}
	var fallback Info
	hasFallback := false
	for _, info := range r.mgr.List() {
		if info.State != StateRunning || strings.ToLower(strings.TrimSpace(info.Kind)) != kind {
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

func (r *Router) sendError(send func(Frame), streamID, message string) {
	send(Frame{Type: FrameError, StreamID: streamID, Error: message, Data: []byte(message)})
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
