package proc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNoRawStream is returned when a unit has no byte-exact stream, or when one
// has already been claimed. A raw stream has exactly one owner by design: there
// is no fan-out, so back pressure works and nothing is replayed or dropped.
var ErrNoRawStream = errors.New("proc: no unclaimed raw stream")

type session struct {
	Info

	output   *OutputBuffer
	attached *Attached

	done   chan struct{} // closed once the unit is finished and committed
	exited chan struct{} // closed once Attached.Wait returned
	result Result        // written before exited closes

	cancel  context.CancelFunc
	stopReq chan StopOptions // capacity 1; the supervisor owns the ladder

	// terminal is the state this unit is headed for once it stops. The
	// supervisor knows the category at the moment it decides to stop -- a stop
	// request is a kill, a failed probe is a failure -- so it records it there
	// rather than trying to infer it from the exit afterwards.
	terminal State

	peekOff   int64
	rawClaim  sync.Once
	rawStream io.ReadCloser
}

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
	onDone   func(Info)
	onEvent  func(Event)
	bufCap   int

	// abandonGrace is how long the supervisor waits for a unit that ignores
	// every stop rung before recording it as abandoned. It is per-manager so
	// that a test can exercise that path without a real wait.
	abandonGrace time.Duration
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*session), abandonGrace: defaultAbandonGrace}
}

func (m *Manager) abandonAfter() time.Duration {
	if m.abandonGrace > 0 {
		return m.abandonGrace
	}
	return defaultAbandonGrace
}

func (m *Manager) SetOnEvent(fn func(Event)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEvent = fn
}

func (m *Manager) SetOnDone(fn func(Info)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onDone = fn
}

// Start registers one unit and begins supervising it. The unit's lifetime is
// bounded by ctx and by spec.Timeout; a caller that wants a unit to outlive the
// call that created it passes a detached context.
func (m *Manager) Start(ctx context.Context, spec Spec, attachment Attachment) (Info, error) {
	if attachment == nil {
		return Info{}, errors.New("proc: attachment required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	id, err := genID()
	if err != nil {
		return Info{}, err
	}
	buffer, err := m.newBuffer(spec)
	if err != nil {
		return Info{}, err
	}

	runCtx, cancel := context.WithCancel(ctx)
	if spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	}

	attached, err := attachment.Start(runCtx)
	if err != nil {
		cancel()
		buffer.Close()
		return Info{}, err
	}
	if attached == nil || attached.Wait == nil {
		cancel()
		buffer.Close()
		return Info{}, errors.New("proc: attachment returned no Wait")
	}

	name := spec.Name
	if name == "" {
		name = labelFromCommand(spec.Command)
	}
	kind := spec.Kind
	if kind == "" {
		kind = "task"
	}
	state := StateRunning
	if attached.Ready != nil {
		state = StateStarting
	}
	startedAt := time.Now()

	s := &session{
		Info: Info{
			ID:             id,
			Shape:          attached.Shape,
			Kind:           kind,
			Name:           name,
			Command:        spec.Command,
			StartedAt:      startedAt,
			LastActivityAt: startedAt,
			ActivitySeq:    1,
			State:          state,
			Proc:           attached.Proc,
		},
		output:    buffer,
		attached:  attached,
		done:      make(chan struct{}),
		exited:    make(chan struct{}),
		cancel:    cancel,
		stopReq:   make(chan StopOptions, 1),
		rawStream: attached.Raw,
	}

	m.mu.Lock()
	m.sessions[id] = s
	info := s.Info
	m.mu.Unlock()

	m.watchOutput(s)
	m.emit(Event{Action: EventSessionCreated, Info: info})

	go func() {
		s.result = attached.Wait()
		close(s.exited)
	}()
	go m.supervise(s, runCtx, spec)

	return info, nil
}

// supervise is the only lifecycle loop. Every shape, every deadline, every stop
// request and every probe failure passes through here.
func (m *Manager) supervise(s *session, ctx context.Context, spec Spec) {
	defer s.cancel()
	attached := s.attached
	pumps := m.startPumps(s, attached)

	if attached.Ready != nil {
		if err := attached.Ready(ctx); err != nil {
			m.stop(s, StateFailed, StopOptions{Reason: "not ready: " + err.Error()})
			m.finish(s, m.awaitExit(s), pumps)
			return
		}
		m.markReady(s)
	}

	var tick <-chan time.Time
	if attached.Live != nil {
		interval := spec.LiveEvery
		if interval <= 0 {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		tick = ticker.C
	}

	var result Result
	for {
		select {
		case <-s.exited:
			result = s.result
		case <-ctx.Done():
			m.stop(s, StateKilled, StopOptions{Reason: reasonFromContext(ctx, spec.Timeout)})
			result = m.awaitExit(s)
		case request := <-s.stopReq:
			m.stop(s, StateKilled, request)
			result = m.awaitExit(s)
		case <-tick:
			if err := attached.Live(ctx); err == nil {
				continue
			} else {
				m.stop(s, StateFailed, StopOptions{Reason: "liveness: " + err.Error()})
				result = m.awaitExit(s)
			}
		}
		break
	}
	m.finish(s, result, pumps)
}

// stop is the only stop ladder. Cancelling the unit's context is the universal
// rung: it is decisive for in-process work and inert for an OS process, which
// then gets the signals it understands.
func (m *Manager) stop(s *session, terminal State, request StopOptions) {
	m.setTerminal(s, terminal, request.Reason)
	s.cancel()
	if s.attached.Signal == nil {
		return
	}
	grace := request.Grace
	if grace <= 0 {
		grace = killGrace
	}
	for _, sig := range []Signal{SigInterrupt, SigTerminate, SigKill} {
		if sig < request.From {
			continue
		}
		if err := s.attached.Signal(sig); err != nil {
			// This rung is unavailable on this platform or for this shape.
			// Escalate immediately rather than waiting out its grace period.
			continue
		}
		if waitClosed(s.exited, grace) {
			break
		}
	}
	// The tracked process is gone, but anything it backgrounded is still
	// holding its process group open -- and a non-interactive shell makes
	// background jobs ignore the polite rungs, so they routinely survive one.
	// Whatever is left belongs to a unit we were told to stop, so sweep it.
	if s.attached.Proc != nil {
		_ = s.attached.Signal(SigKill)
	}
}

// awaitExit is bounded. A unit that ignores every rung would otherwise block
// this supervisor forever; the registry gives up, records the truth and lets
// the caller see an abandoned unit instead of a permanently running one.
func (m *Manager) awaitExit(s *session) Result {
	if waitClosed(s.exited, m.abandonAfter()) {
		return s.result
	}
	return Result{Err: errAbandoned}
}

var errAbandoned = errors.New("proc: unit did not exit")

// finish is the only completion path. The order matters: Close is what makes a
// blocked Read return, so it has to precede the drain.
func (m *Manager) finish(s *session, result Result, pumps []<-chan struct{}) {
	// Drain first: the unit has exited, so its stream is already at EOF or
	// about to be, and closing the attachment before the pump catches up would
	// truncate the last thing it said.
	for _, pump := range pumps {
		waitClosed(pump, outputDrainGrace)
	}
	// Then close, which is what unblocks a read that will never see EOF --
	// a terminal with no other reader, or a descendant holding the write end.
	if s.attached.Close != nil {
		_ = s.attached.Close()
	}
	for _, pump := range pumps {
		waitClosed(pump, m.abandonAfter())
	}

	state, reason := m.classify(s, result)

	m.mu.Lock()
	endedAt := time.Now()
	s.EndedAt = endedAt
	s.LastActivityAt = endedAt
	s.ActivitySeq++
	s.State = state
	s.Reason = reason
	s.Status = result.Status
	if s.Proc != nil && result.Exited {
		s.Proc.ExitCode = result.ExitCode
		s.Proc.Signal = result.Signal
	}
	info := s.Info
	m.mu.Unlock()

	s.output.Close()
	close(s.done)
	m.emit(Event{Action: EventSessionClosed, Info: info})

	m.mu.Lock()
	onDone := m.onDone
	m.mu.Unlock()
	if onDone != nil {
		defer func() { _ = recover() }()
		onDone(info)
	}
}

// classify turns a terminal Result into a state without inventing an exit code.
func (m *Manager) classify(s *session, result Result) (State, string) {
	m.mu.Lock()
	terminal, reason := s.terminal, s.Reason
	m.mu.Unlock()
	if terminal != "" {
		return terminal, reason
	}
	switch {
	case result.Err == nil:
		return StateCompleted, ""
	case errors.Is(result.Err, context.DeadlineExceeded):
		return StateKilled, "timeout"
	case errors.Is(result.Err, context.Canceled):
		return StateKilled, "canceled"
	case errors.Is(result.Err, errAbandoned):
		return StateKilled, "abandoned"
	case result.Status != nil:
		if *result.Status == 0 {
			return StateCompleted, ""
		}
		return StateFailed, ""
	case result.Exited:
		return StateFailed, ""
	default:
		return StateFailed, result.Err.Error()
	}
}

// reasonFromContext names the deadline that was missed, not just the fact of
// it: "timeout" alone leaves the reader guessing which limit applied.
func reasonFromContext(ctx context.Context, timeout time.Duration) string {
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "canceled"
	}
	if timeout > 0 {
		return "timeout after " + timeout.String()
	}
	return "timeout"
}

func (m *Manager) startPumps(s *session, attached *Attached) []<-chan struct{} {
	if attached.Output == nil {
		return nil
	}
	return []<-chan struct{}{pumpOutput(attached.Output, s.output)}
}

func (m *Manager) markReady(s *session) {
	m.mu.Lock()
	now := time.Now()
	s.ReadyAt = now
	s.LastActivityAt = now
	s.ActivitySeq++
	s.State = StateRunning
	info := s.Info
	m.mu.Unlock()
	m.emit(Event{Action: EventSessionUpdated, Info: info})
}

// setTerminal records where the unit is headed. The first decision wins: an
// escalating ladder must not overwrite the reason that started it.
func (m *Manager) setTerminal(s *session, terminal State, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.terminal == "" {
		s.terminal = terminal
	}
	if s.Reason == "" {
		s.Reason = reason
	}
}

// waitClosed reports whether ch closed within d.
func waitClosed(ch <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	}
}

// --- lifecycle control ---

// Stop asks a unit to end, escalating from request.From through the ladder.
func (m *Manager) Stop(id string, request StopOptions) error {
	m.mu.Lock()
	s := m.resolve(id)
	m.mu.Unlock()
	if s == nil {
		return fmt.Errorf("no such session: %s", id)
	}
	select {
	case <-s.done:
		return nil
	default:
	}
	if request.Reason == "" {
		request.Reason = "killed by user"
	}
	select {
	case s.stopReq <- request:
	default:
		// A stop is already in flight; the ladder is running.
	}
	return nil
}

// Kill stops a unit without waiting for it to go quietly.
func (m *Manager) Kill(id string) error {
	return m.Stop(id, StopOptions{From: SigTerminate})
}

// Shutdown stops every live unit and waits for them, bounded.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	live := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		select {
		case <-s.done:
		default:
			live = append(live, s)
		}
	}
	m.mu.Unlock()

	for _, s := range live {
		_ = m.Stop(s.ID, StopOptions{Reason: "shutdown", Grace: shutdownGrace})
	}
	deadline := time.After(killGrace + shutdownGrace)
	for _, s := range live {
		select {
		case <-s.done:
		case <-deadline:
			return
		}
	}
}

// Raw hands a unit's byte-exact stream to its single owner.
func (m *Manager) Raw(id string) (io.ReadCloser, error) {
	m.mu.Lock()
	s := m.resolve(id)
	m.mu.Unlock()
	if s == nil {
		return nil, fmt.Errorf("no such session: %s", id)
	}
	var stream io.ReadCloser
	s.rawClaim.Do(func() { stream = s.rawStream })
	if stream == nil {
		return nil, ErrNoRawStream
	}
	return stream, nil
}

func (m *Manager) Write(id string, data []byte) error {
	m.mu.Lock()
	s := m.resolve(id)
	m.mu.Unlock()
	if s == nil {
		return fmt.Errorf("no such session: %s", id)
	}
	select {
	case <-s.done:
		return fmt.Errorf("session %s already finished", id)
	default:
	}
	if s.attached.Input == nil {
		return fmt.Errorf("session %s does not accept input", id)
	}
	_, err := s.attached.Input.Write(data)
	return err
}

func (m *Manager) Resize(id string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	m.mu.Lock()
	s := m.resolve(id)
	m.mu.Unlock()
	if s == nil {
		return fmt.Errorf("no such session: %s", id)
	}
	select {
	case <-s.done:
		return nil
	default:
	}
	if s.attached.Resize == nil {
		return fmt.Errorf("session %s is not resizable", id)
	}
	if err := callResize(s.attached.Resize, cols, rows); err != nil {
		return err
	}
	info, _ := m.touchSession(id, 0)
	m.emit(Event{Action: EventSessionUpdated, Info: info})
	return nil
}

func callResize(resize func(cols, rows int) error, cols, rows int) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("resize callback panic: %v", recovered)
		}
	}()
	return resize(cols, rows)
}

func (m *Manager) Wait(ctx context.Context, id string, timeout time.Duration) (Info, error) {
	m.mu.Lock()
	s := m.resolve(id)
	m.mu.Unlock()
	if s == nil {
		return Info{}, fmt.Errorf("no such session: %s", id)
	}
	var timerC <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timerC = timer.C
	}
	var ctxErr error
	select {
	case <-s.done:
	case <-timerC:
	case <-ctx.Done():
		ctxErr = ctx.Err()
	}
	m.mu.Lock()
	info := s.Info
	m.mu.Unlock()
	return info, ctxErr
}

// --- query ---

func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.Info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

func (m *Manager) Get(id string) (Info, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.resolve(id)
	if s == nil {
		return Info{}, false
	}
	return s.Info, true
}

func (m *Manager) SetKind(id, kind string) {
	if kind == "" {
		return
	}
	var info Info
	m.mu.Lock()
	if s := m.resolve(id); s != nil {
		s.Kind = kind
		s.LastActivityAt = time.Now()
		s.ActivitySeq++
		info = s.Info
	}
	m.mu.Unlock()
	if info.ID != "" {
		m.emit(Event{Action: EventSessionUpdated, Info: info})
	}
}

// resolve finds a session by ID first, then by name.
func (m *Manager) resolve(idOrName string) *session {
	if s, ok := m.sessions[idOrName]; ok {
		return s
	}
	for _, s := range m.sessions {
		if s.Name == idOrName {
			return s
		}
	}
	return nil
}

// RunningCount counts units that may still do work, which includes units
// waiting on their readiness probe.
func (m *Manager) RunningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sessions {
		if s.State.Active() {
			n++
		}
	}
	return n
}

func (m *Manager) Done(id string) <-chan struct{} {
	m.mu.Lock()
	s := m.resolve(id)
	m.mu.Unlock()
	if s == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return s.done
}

func (m *Manager) Peek(id string, n int) (string, error) {
	s, err := m.lookup(id)
	if err != nil {
		return "", err
	}
	if n <= 0 {
		n = 30
	}
	return s.output.TailLines(n), nil
}

func (m *Manager) PeekBytes(id string, n int) (string, error) {
	s, err := m.lookup(id)
	if err != nil {
		return "", err
	}
	return s.output.TailBytes(n), nil
}

// SnapshotBytes returns the last n bytes and the current output offset.
func (m *Manager) SnapshotBytes(id string, n int) ([]byte, int64, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, 0, err
	}
	data, offset := s.output.TailRawBytesWithOffset(n)
	return data, offset, nil
}

func (m *Manager) OutputLen(id string) (int64, error) {
	s, err := m.lookup(id)
	if err != nil {
		return 0, err
	}
	return s.output.Len(), nil
}

func (m *Manager) PeekOrEmpty(id string, n int) string {
	out, _ := m.Peek(id, n)
	return out
}

const defaultPeekNewMax int64 = 40 * 1024

func (m *Manager) PeekNew(id string, maxBytes int64) (string, bool, error) {
	s, err := m.lookup(id)
	if err != nil {
		return "", false, err
	}
	if maxBytes <= 0 {
		maxBytes = defaultPeekNewMax
	}
	m.mu.Lock()
	offset := s.peekOff
	m.mu.Unlock()

	data, newOffset, more, err := s.output.ReadSinceLimit(offset, maxBytes)
	if err != nil {
		return "", false, err
	}

	m.mu.Lock()
	s.peekOff = newOffset
	m.mu.Unlock()

	return string(data), more, nil
}

// ReadFrom reads output since the given offset without modifying session state.
// The caller tracks the returned offset for subsequent calls.
func (m *Manager) ReadFrom(id string, offset int64, maxBytes int64) (string, int64, error) {
	data, newOffset, err := m.ReadBytesFrom(id, offset, maxBytes)
	if err != nil {
		return "", offset, err
	}
	return string(data), newOffset, nil
}

// ReadBytesFrom reads output bytes since the given offset without modifying
// session state.
func (m *Manager) ReadBytesFrom(id string, offset int64, maxBytes int64) ([]byte, int64, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, 0, err
	}
	if maxBytes <= 0 {
		maxBytes = defaultPeekNewMax
	}
	data, newOffset, _, err := s.output.ReadSinceLimit(offset, maxBytes)
	if err != nil {
		return nil, offset, err
	}
	return data, newOffset, nil
}

// Monitor starts a goroutine that periodically reads incremental output and
// calls push with new content. It stops when the unit ends.
func (m *Manager) Monitor(id string, interval time.Duration, push func(output string)) {
	_ = m.MonitorFrom(context.Background(), id, 0, interval, func(output []byte) {
		push(string(output))
	})
}

// MonitorFrom starts a cancelable incremental output monitor at offset.
func (m *Manager) MonitorFrom(ctx context.Context, id string, offset int64, interval time.Duration, push func(output []byte)) error {
	s, err := m.lookup(id)
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.done:
				if data, _, _ := m.ReadBytesFrom(id, offset, 0); len(data) > 0 {
					push(data)
				}
				return
			case <-ticker.C:
				data, newOffset, err := m.ReadBytesFrom(id, offset, 0)
				if err != nil {
					return
				}
				offset = newOffset
				if len(data) > 0 {
					push(data)
				}
			}
		}
	}()
	return nil
}

// --- helpers ---

func (m *Manager) lookup(id string) (*session, error) {
	m.mu.Lock()
	s := m.resolve(id)
	m.mu.Unlock()
	if s == nil {
		return nil, fmt.Errorf("no such session: %s", id)
	}
	return s, nil
}

func (m *Manager) watchOutput(s *session) {
	if s == nil || s.output == nil {
		return
	}
	id := s.ID
	s.output.SetOnWrite(func(p []byte) {
		if len(p) == 0 {
			return
		}
		if info, ok := m.touchSession(id, len(p)); ok {
			m.emit(Event{Action: EventSessionOutput, Info: info, OutputBytes: len(p)})
		}
	})
}

func (m *Manager) touchSession(id string, outputBytes int) (Info, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.resolve(id)
	if s == nil {
		return Info{}, false
	}
	s.LastActivityAt = time.Now()
	s.ActivitySeq++
	s.OutputBytes += int64(outputBytes)
	return s.Info, true
}

func (m *Manager) emit(ev Event) {
	if ev.Action == "" || ev.Info.ID == "" {
		return
	}
	m.mu.Lock()
	onEvent := m.onEvent
	m.mu.Unlock()
	if onEvent != nil {
		onEvent(ev)
	}
}

func (m *Manager) bufferCap(spec Spec) int {
	if spec.BufferCap > 0 {
		return spec.BufferCap
	}
	if m.bufCap > 0 {
		return m.bufCap
	}
	return DefaultBufferCap
}

func (m *Manager) newBuffer(spec Spec) (*OutputBuffer, error) {
	size := m.bufferCap(spec)
	buffer := &OutputBuffer{
		buf:       make([]byte, 0, min(size, 64*1024)),
		cap:       size,
		stripANSI: spec.StripANSI,
	}
	if spec.OutputFile != "" {
		file, err := os.OpenFile(spec.OutputFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open output file: %w", err)
		}
		buffer.file = file
	}
	return buffer, nil
}

func genID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func labelFromCommand(cmdLine string) string {
	cmdLine = strings.TrimSpace(cmdLine)
	if i := strings.IndexAny(cmdLine, " \t\n"); i > 0 {
		cmdLine = cmdLine[:i]
	}
	if i := strings.LastIndex(cmdLine, "/"); i >= 0 {
		cmdLine = cmdLine[i+1:]
	}
	if cmdLine == "" {
		return "task"
	}
	return cmdLine
}
