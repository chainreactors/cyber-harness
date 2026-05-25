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
	ModeInbox       LoopMode = iota // fire → push to inbox, processed by current agent turn
	ModeIndependent                 // fire → call OnFire callback independently
)

type LoopEntry struct {
	Name      string
	Interval  time.Duration
	Prompt    string
	Mode      LoopMode
	Immediate bool // fire once immediately on creation
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
	mu    sync.Mutex
	loops map[string]*loopState
	inbox inbox.Inbox
	log   telemetry.Logger
}

type loopState struct {
	entry     LoopEntry
	cancel    context.CancelFunc
	fireCount int
	lastFired time.Time
}

func NewLoopScheduler(ib inbox.Inbox, logger telemetry.Logger) *LoopScheduler {
	return &LoopScheduler{
		loops: make(map[string]*loopState),
		inbox: ib,
		log:   logger,
	}
}

const minLoopInterval = 10 * time.Second

func (s *LoopScheduler) Add(ctx context.Context, entry LoopEntry) error {
	if strings.TrimSpace(entry.Name) == "" {
		return fmt.Errorf("loop name is required")
	}
	if entry.Interval < minLoopInterval {
		return fmt.Errorf("interval must be >= %s", minLoopInterval)
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
	if _, exists := s.loops[entry.Name]; exists {
		s.mu.Unlock()
		return fmt.Errorf("loop %q already exists", entry.Name)
	}

	loopCtx, cancel := context.WithCancel(ctx)
	state := &loopState{
		entry:  entry,
		cancel: cancel,
	}
	s.loops[entry.Name] = state
	s.mu.Unlock()

	s.log.Importantf("loop=%s interval=%s mode=%d created", entry.Name, entry.Interval, entry.Mode)

	if entry.Immediate {
		s.fire(loopCtx, state)
	}

	go s.ticker(loopCtx, state)

	return nil
}

func (s *LoopScheduler) ticker(ctx context.Context, state *loopState) {
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
		body := entry.Prompt
		if entry.OnFire != nil {
			result, err := entry.OnFire(ctx, entry)
			if err != nil {
				s.log.Warnf("loop=%s fire=%d OnFire error: %s", entry.Name, count, err)
				return
			}
			if result != "" {
				body = result
			}
		}
		content := fmt.Sprintf("<loop_fire name=%q interval=%q fire_count=%d>\n%s\n</loop_fire>",
			entry.Name, entry.Interval, count, body)
		msg := inbox.NewMessage(inbox.OriginSystem, "user", content)
		msg.Priority = inbox.PriorityLow
		msg.Meta = map[string]any{
			"loop_name":  entry.Name,
			"fire_count": count,
		}
		if err := s.inbox.Push(msg); err != nil {
			s.log.Warnf("loop=%s fire=%d inbox push failed: %s", entry.Name, count, err)
		} else {
			s.log.Debugf("loop=%s fire=%d pushed to inbox", entry.Name, count)
		}

	case ModeIndependent:
		go func() {
			result, err := entry.OnFire(ctx, entry)
			if err != nil {
				s.log.Warnf("loop=%s fire=%d independent execution failed: %s", entry.Name, count, err)
				return
			}
			s.log.Debugf("loop=%s fire=%d independent execution completed (len=%d)", entry.Name, count, len(result))
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
