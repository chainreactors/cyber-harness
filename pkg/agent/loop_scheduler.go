package agent

import (
	"context"
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
//   - ModeInbox requires Prompt (static content injected into inbox).
//   - ModeIndependent requires OnFire (called each interval in a goroutine).
type LoopEntry struct {
	Name      string
	Interval  time.Duration
	Prompt    string
	Mode      LoopMode
	Immediate bool
	OnFire    func(ctx context.Context, entry LoopEntry) (string, error)
	CreatedAt time.Time
}

type LoopInfo struct {
	Name      string        `json:"name"`
	Prompt    string        `json:"prompt"`
	Interval  time.Duration `json:"interval"`
	Mode      LoopMode      `json:"mode"`
	FireCount int           `json:"fire_count"`
	LastFired time.Time     `json:"last_fired,omitempty"`
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

func (s *LoopScheduler) Add(ctx context.Context, entry LoopEntry) error {
	if strings.TrimSpace(entry.Name) == "" {
		return fmt.Errorf("loop name is required")
	}
	if entry.Mode == ModeIndependent && entry.OnFire == nil {
		return fmt.Errorf("OnFire callback is required for ModeIndependent")
	}
	if entry.Mode == ModeInbox && strings.TrimSpace(entry.Prompt) == "" {
		return fmt.Errorf("prompt is required for ModeInbox")
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}

	s.mu.Lock()
	if entry.Interval < s.minInterval {
		s.mu.Unlock()
		return fmt.Errorf("interval must be >= %s", s.minInterval)
	}
	if _, exists := s.loops[entry.Name]; exists {
		s.mu.Unlock()
		return fmt.Errorf("loop %q already exists", entry.Name)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	state := &loopState{entry: entry, cancel: cancel}
	s.loops[entry.Name] = state
	s.mu.Unlock()

	s.log.Importantf("loop=%s interval=%s mode=%d created", entry.Name, entry.Interval, entry.Mode)

	if entry.Immediate {
		s.fire(loopCtx, state)
	}
	go s.run(loopCtx, state)
	return nil
}

func (s *LoopScheduler) run(ctx context.Context, state *loopState) {
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

func (s *LoopScheduler) fire(ctx context.Context, state *loopState) {
	s.mu.Lock()
	state.fireCount++
	state.lastFired = time.Now()
	count := state.fireCount
	entry := state.entry
	s.mu.Unlock()

	switch entry.Mode {
	case ModeInbox:
		content := fmt.Sprintf("<loop_fire name=%q interval=%q fire_count=%d>\n%s\n</loop_fire>",
			entry.Name, entry.Interval, count, entry.Prompt)
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
			Interval:  state.entry.Interval,
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
