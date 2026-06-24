package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type LoopMode int

const (
	// ModeInbox pushes LoopEntry.Prompt to the inbox as a system message.
	// The agent's turn loop drains it and lets the LLM decide what to do.
	ModeInbox LoopMode = iota

	// ModeIndependent calls LoopEntry.OnFire in a goroutine.
	// Used for work that needs its own agent run (e.g. swarm heartbeat).
	ModeIndependent
)

// LoopEntry defines a single recurring task.
//
// Schedule priority: Cron > Interval.
//   - If Cron is set, it drives scheduling (Interval is ignored).
//   - If only Interval is set, it is used as a simple ticker.
//   - ModeInbox requires Prompt.
//   - ModeIndependent requires OnFire.
type LoopEntry struct {
	Name      string
	Cron      *CronExpr
	Interval  time.Duration
	Prompt    string
	Mode      LoopMode
	Immediate bool
	OnFire    func(ctx context.Context, entry LoopEntry) (string, error)
	CreatedAt time.Time
}

// Schedule returns a human-readable string for the schedule.
func (e LoopEntry) Schedule() string {
	if e.Cron != nil {
		return e.Cron.String()
	}
	return e.Interval.String()
}

type LoopInfo struct {
	Name      string `json:"name"`
	Prompt    string `json:"prompt"`
	Schedule  string `json:"schedule"`
	Mode      LoopMode `json:"mode"`
	FireCount int      `json:"fire_count"`
	LastFired time.Time `json:"last_fired,omitempty"`
}

type LoopScheduler struct {
	mu          sync.Mutex
	loops       map[string]*loopState
	inbox       inbox.Inbox
	log         telemetry.Logger
	minInterval time.Duration
}

type loopState struct {
	entry     LoopEntry
	cancel    context.CancelFunc
	fireCount int
	lastFired time.Time
}

const DefaultMinLoopInterval = 10 * time.Second

func NewLoopScheduler(ib inbox.Inbox, logger telemetry.Logger) *LoopScheduler {
	return &LoopScheduler{
		loops:       make(map[string]*loopState),
		inbox:       ib,
		log:         logger,
		minInterval: DefaultMinLoopInterval,
	}
}

func (s *LoopScheduler) SetMinInterval(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.minInterval = d
}

func (s *LoopScheduler) Add(ctx context.Context, entry LoopEntry) (string, error) {
	if entry.Mode == ModeIndependent && entry.OnFire == nil {
		return "", fmt.Errorf("OnFire callback is required for ModeIndependent")
	}
	if entry.Mode == ModeInbox && strings.TrimSpace(entry.Prompt) == "" {
		return "", fmt.Errorf("prompt is required for ModeInbox")
	}
	if entry.Cron == nil && entry.Interval == 0 {
		return "", fmt.Errorf("either Cron or Interval is required")
	}
	if entry.Cron == nil && entry.Interval < s.minInterval {
		return "", fmt.Errorf("interval must be >= %s", s.minInterval)
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	if strings.TrimSpace(entry.Name) == "" {
		entry.Name = autoName(entry.Prompt)
	}

	s.mu.Lock()
	if _, exists := s.loops[entry.Name]; exists {
		s.mu.Unlock()
		return "", fmt.Errorf("loop %q already exists", entry.Name)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	state := &loopState{entry: entry, cancel: cancel}
	s.loops[entry.Name] = state
	s.mu.Unlock()

	s.log.Importantf("loop=%s schedule=%s mode=%d created", entry.Name, entry.Schedule(), entry.Mode)

	if entry.Immediate {
		s.fire(loopCtx, state)
	}
	go s.run(loopCtx, state)
	return entry.Name, nil
}

func autoName(prompt string) string {
	h := sha256.Sum256([]byte(prompt))
	return fmt.Sprintf("loop-%x", h[:4])
}

func (s *LoopScheduler) run(ctx context.Context, state *loopState) {
	if state.entry.Cron != nil {
		s.runCron(ctx, state)
	} else {
		s.runTicker(ctx, state)
	}
}

func (s *LoopScheduler) runTicker(ctx context.Context, state *loopState) {
	t := time.NewTicker(state.entry.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.fire(ctx, state)
		}
	}
}

func (s *LoopScheduler) runCron(ctx context.Context, state *loopState) {
	for {
		next := state.entry.Cron.Next(time.Now())
		if next.IsZero() {
			s.log.Warnf("loop=%s cron has no next fire time", state.entry.Name)
			return
		}
		delay := time.Until(next)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.fire(ctx, state)
		}
	}
}

func (s *LoopScheduler) fire(ctx context.Context, state *loopState) {
	s.mu.Lock()
	state.fireCount++
	state.lastFired = time.Now()
	count := state.fireCount
	entry := state.entry
	s.mu.Unlock()

	switch entry.Mode {
	case ModeInbox:
		content := fmt.Sprintf("<loop_fire name=%q schedule=%q fire_count=%d>\n%s\n</loop_fire>",
			entry.Name, entry.Schedule(), count, entry.Prompt)
		msg := inbox.NewMessage(inbox.OriginSystem, "user", content)
		msg.Priority = inbox.PriorityLow
		msg.Meta = map[string]any{
			"loop_name":  entry.Name,
			"fire_count": count,
		}
		if err := s.inbox.Push(msg); err != nil {
			s.log.Warnf("loop=%s fire=%d inbox push failed: %s", entry.Name, count, err)
		}

	case ModeIndependent:
		go func() {
			if _, err := entry.OnFire(ctx, entry); err != nil {
				s.log.Warnf("loop=%s fire=%d failed: %s", entry.Name, count, err)
			}
		}()
	}
}

func (s *LoopScheduler) Remove(name string) error {
	s.mu.Lock()
	state, ok := s.loops[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("loop %q not found", name)
	}
	state.cancel()
	delete(s.loops, name)
	s.mu.Unlock()
	s.log.Importantf("loop=%s deleted", name)
	return nil
}

func (s *LoopScheduler) List() []LoopInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]LoopInfo, 0, len(s.loops))
	for _, state := range s.loops {
		result = append(result, LoopInfo{
			Name:      state.entry.Name,
			Prompt:    state.entry.Prompt,
			Schedule:  state.entry.Schedule(),
			Mode:      state.entry.Mode,
			FireCount: state.fireCount,
			LastFired: state.lastFired,
		})
	}
	return result
}

func (s *LoopScheduler) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.loops)
}

func (s *LoopScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, state := range s.loops {
		state.cancel()
		delete(s.loops, name)
	}
}
