package record

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/tool"
)

type recordingSession struct {
	mu     sync.RWMutex
	info   SessionInfo
	cancel context.CancelFunc
	done   chan struct{}
}

func (t *Tool) start(ctx context.Context, args Args, duration time.Duration) (*recordingSession, error) {
	if t.backend == nil {
		return nil, fmt.Errorf("capture backend is unavailable")
	}
	req, err := normalizeCaptureArgs(args)
	if err != nil {
		return nil, err
	}
	target, err := callBackendResolve(t.backend, ctx, req)
	if err != nil {
		return nil, fmt.Errorf("resolve capture target: %w", err)
	}
	id := newID()
	path, err := t.outputPath(ctx, args.Output, "record-"+id, ".mp4")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create recording directory: %w", err)
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, fmt.Errorf("record tool is closed")
	}
	if t.activeCountLocked() >= t.maxConcurrent {
		ids := t.activeIDsLocked()
		t.mu.Unlock()
		return nil, fmt.Errorf("recording concurrency limit %d reached (active: %s)", t.maxConcurrent, strings.Join(ids, ", "))
	}
	for _, existing := range t.sessions {
		info := existing.snapshot()
		if isActive(info.State) && samePath(info.Output, path) {
			t.mu.Unlock()
			return nil, fmt.Errorf("output path is already used by recording %s", info.RecordingID)
		}
	}

	runCtx, cancel := context.WithCancel(context.Background())
	if duration > 0 {
		runCtx, cancel = context.WithTimeout(context.Background(), duration)
	}
	session := &recordingSession{
		info: SessionInfo{
			RecordingID: id,
			State:       sessionStarting,
			Target:      target.Info,
			Output:      path,
			FPS:         req.FPS,
		},
		cancel: cancel,
		done:   make(chan struct{}),
	}
	t.sessions[id] = session
	t.mu.Unlock()

	go t.runSession(runCtx, session, target)
	return session, nil
}

func (t *Tool) runSession(ctx context.Context, session *recordingSession, target resolvedTarget) {
	defer session.cancel()
	started := time.Now().UTC()
	session.update(func(info *SessionInfo) {
		if info.State == sessionStarting {
			info.State = sessionRecording
		}
		info.StartedAt = &started
	})
	snapshot := session.snapshot()
	media, recordErr := callBackendRecord(t.backend, ctx, target, snapshot.Output, snapshot.FPS)
	ended := time.Now().UTC()
	var size int64
	if stat, err := os.Stat(snapshot.Output); err == nil {
		size = stat.Size()
		if recordErr == nil && size == 0 {
			recordErr = fmt.Errorf("recording output is empty")
		}
	} else if recordErr == nil {
		recordErr = fmt.Errorf("inspect recording output: %w", err)
	}
	session.update(func(info *SessionInfo) {
		info.EndedAt = &ended
		info.Bytes = size
		info.Frames = media.Frames
		if media.Width > 0 {
			info.Target.Width = media.Width
		}
		if media.Height > 0 {
			info.Target.Height = media.Height
		}
		if info.StartedAt != nil {
			info.DurationMS = ended.Sub(*info.StartedAt).Milliseconds()
		}
		if recordErr != nil {
			info.State = sessionFailed
			info.Error = recordErr.Error()
		} else {
			info.State = sessionCompleted
		}
	})
	t.pruneSessionHistory()
	close(session.done)
}

func callBackendRecord(backend captureBackend, ctx context.Context, target resolvedTarget, output string, fps int) (media mediaInfo, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("capture backend panicked: %v", recovered)
		}
	}()
	return backend.Record(ctx, target, output, fps)
}

func callBackendResolve(backend captureBackend, ctx context.Context, req captureRequest) (target resolvedTarget, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("capture backend panicked while resolving target: %v", recovered)
		}
	}()
	return backend.Resolve(ctx, req)
}

func callBackendScreenshot(backend captureBackend, ctx context.Context, target resolvedTarget) (img image.Image, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("capture backend panicked while taking screenshot: %v", recovered)
		}
	}()
	return backend.Screenshot(ctx, target)
}

func (t *Tool) stop(ctx context.Context, id string) (*tool.Result, error) {
	if id == "" {
		return nil, fmt.Errorf("recording_id is required for action=stop")
	}
	session, ok := t.session(id)
	if !ok {
		return nil, fmt.Errorf("recording %q not found", id)
	}
	if isActive(session.snapshot().State) {
		session.update(func(info *SessionInfo) { info.State = sessionStopping })
		session.cancel()
	}
	return t.waitResult(ctx, session)
}

func (t *Tool) waitResult(ctx context.Context, session *recordingSession) (*tool.Result, error) {
	select {
	case <-session.done:
		info := session.snapshot()
		if info.State == sessionFailed {
			return tool.ErrorResult(marshalJSON(info)), nil
		}
		text := marshalJSON(info)
		return &tool.Result{Output: []*aop.Content{
			aop.Text(text),
			aop.MediaURI("video", "video/mp4", filepath.Base(info.Output), t.mediaURI(ctx, info.Output)),
		}}, nil
	case <-ctx.Done():
		session.cancel()
		timer := time.NewTimer(sessionStopTimeout)
		defer timer.Stop()
		select {
		case <-session.done:
			return nil, ctx.Err()
		case <-timer.C:
			return nil, fmt.Errorf("stop recording after context cancellation: %w", ctx.Err())
		}
	}
}

func (t *Tool) status(id string) (*tool.Result, error) {
	if id != "" {
		session, ok := t.session(id)
		if !ok {
			return nil, fmt.Errorf("recording %q not found", id)
		}
		return jsonResult(session.snapshot())
	}
	t.mu.RLock()
	infos := make([]SessionInfo, 0, len(t.sessions))
	for _, session := range t.sessions {
		infos = append(infos, session.snapshot())
	}
	t.mu.RUnlock()
	sort.Slice(infos, func(i, j int) bool { return infos[i].RecordingID < infos[j].RecordingID })
	return jsonResult(infos)
}

func (t *Tool) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	sessions := make([]*recordingSession, 0, len(t.sessions))
	for _, session := range t.sessions {
		if isActive(session.snapshot().State) {
			session.update(func(info *SessionInfo) { info.State = sessionStopping })
			session.cancel()
			sessions = append(sessions, session)
		}
	}
	t.mu.Unlock()

	if len(sessions) == 0 {
		return
	}
	timer := time.NewTimer(sessionStopTimeout)
	defer timer.Stop()
	for _, session := range sessions {
		select {
		case <-session.done:
		case <-timer.C:
			return
		}
	}
}

func (t *Tool) activeCountLocked() int {
	count := 0
	for _, session := range t.sessions {
		if isActive(session.snapshot().State) {
			count++
		}
	}
	return count
}

func (t *Tool) activeIDsLocked() []string {
	ids := make([]string, 0, t.maxConcurrent)
	for id, session := range t.sessions {
		if isActive(session.snapshot().State) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (t *Tool) session(id string) (*recordingSession, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	session, ok := t.sessions[id]
	return session, ok
}

func (t *Tool) pruneSessionHistory() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.sessions) <= maxSessionHistory {
		return
	}
	type candidate struct {
		id      string
		endedAt time.Time
	}
	candidates := make([]candidate, 0, len(t.sessions))
	for id, session := range t.sessions {
		info := session.snapshot()
		if isActive(info.State) {
			continue
		}
		endedAt := time.Time{}
		if info.EndedAt != nil {
			endedAt = *info.EndedAt
		}
		candidates = append(candidates, candidate{id: id, endedAt: endedAt})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].endedAt.Equal(candidates[j].endedAt) {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].endedAt.Before(candidates[j].endedAt)
	})
	remove := len(t.sessions) - maxSessionHistory
	if remove > len(candidates) {
		remove = len(candidates)
	}
	for _, candidate := range candidates[:remove] {
		delete(t.sessions, candidate.id)
	}
}

func (session *recordingSession) snapshot() SessionInfo {
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.info
}

func (session *recordingSession) update(fn func(*SessionInfo)) {
	session.mu.Lock()
	defer session.mu.Unlock()
	fn(&session.info)
}

func isActive(state string) bool {
	switch state {
	case sessionStarting, sessionRecording, sessionStopping:
		return true
	default:
		return false
	}
}
