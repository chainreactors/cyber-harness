// Package subagent owns named task preparation and execution leases. Agent and
// session execution remain independent of this optional capability.
package subagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/registry"
	"github.com/chainreactors/cyber/core/resource"
	types "github.com/chainreactors/cyber/core/types"
)

type Mode string

const (
	Sync  Mode = "sync"
	Async Mode = "async"
	Fork  Mode = "fork"
)

type Input struct {
	Prompt  string
	Payload any
}

// Subagent is reusable preparation, never the mutable state of a running task.
// Prepare must return a configuration snapshot and a nonempty task, and must
// not start work or allocate resources requiring a separate lifetime owner.
type Subagent struct {
	Name        string
	Description string
	DefaultMode Mode
	Prepare     func(context.Context, agent.Config, Input) (agent.Config, string, error)
}

type Request struct {
	Name    string
	Label   string
	Input   Input
	Mode    Mode
	Timeout time.Duration
}

// Run owns admission and a definition lease until Finish, including backend
// shutdown and completion notification. Config is private to this invocation.
type Run struct {
	Context context.Context
	Config  agent.Config
	Detail  *types.DelegationDetail
	Mode    Mode
	cancel  context.CancelFunc
	finish  func()
	once    sync.Once
}

func (r *Run) Cancel() { r.cancel() }
func (r *Run) Finish() { r.once.Do(r.finish) }

// Registry implements the single Subagent Point. Its owner activates and drains
// it; consumers register definitions or borrow execution through Executor.
type Registry struct {
	store    *registry.Store[Subagent]
	mu       sync.Mutex
	lifetime context.Context
	cancel   context.CancelFunc
	stopping bool
	active   int
	done     chan struct{}
	reserved map[string]bool // Names remain reserved while their old batch drains.
}

// Executor contains no installation or shutdown operations.
type Executor interface {
	Start(context.Context, agent.Config, Request) (*Run, error)
	Execute(context.Context, agent.Config, Request) (*agent.Result, error)
	Catalog() []Subagent
}

func NewRegistry() *Registry {
	return &Registry{store: registry.New[Subagent](), done: make(chan struct{}), reserved: make(map[string]bool)}
}

func (r *Registry) Activate(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping || r.lifetime != nil {
		return registry.ErrUnavailable
	}
	if err := r.store.Activate(ctx); err != nil {
		return err
	}
	r.lifetime, r.cancel = context.WithCancel(ctx)
	return nil
}

func (r *Registry) Add(values ...Subagent) (resource.Handle, error) {
	entries := make([]registry.Value[Subagent], 0, len(values))
	for _, value := range values {
		if value.Prepare == nil {
			return nil, fmt.Errorf("subagent %q requires Prepare", value.Name)
		}
		if value.DefaultMode == "" {
			value.DefaultMode = Sync
		}
		if !validMode(value.DefaultMode) {
			return nil, fmt.Errorf("invalid default mode %q", value.DefaultMode)
		}
		entries = append(entries, registry.Value[Subagent]{Name: value.Name, Value: value})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping || (r.lifetime != nil && r.lifetime.Err() != nil) {
		return nil, registry.ErrUnavailable
	}
	for _, entry := range entries {
		if r.reserved[entry.Name] {
			return nil, fmt.Errorf("%w: %s", registry.ErrDuplicate, entry.Name)
		}
	}
	handle, err := r.store.Add(entries...)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		r.reserved[entry.Name] = true
	}
	releaseNames := sync.OnceFunc(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, entry := range entries {
			delete(r.reserved, entry.Name)
		}
	})
	return resource.HandleFunc(func(ctx context.Context) error {
		if err := handle.Close(ctx); err != nil {
			return err
		}
		releaseNames()
		return nil
	}), nil
}

func (r *Registry) Catalog() []Subagent {
	var values []Subagent
	for _, entry := range r.store.Entries() {
		values = append(values, entry.Value)
	}
	return values
}

func validMode(mode Mode) bool { return mode == Sync || mode == Async || mode == Fork }

// Start prepares once and transfers the lease to the caller. Background tasks
// retain context values, but do not inherit the tool invocation's cancellation.
func (r *Registry) Start(ctx context.Context, cfg agent.Config, request Request) (run *Run, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.lifetime == nil || r.stopping || r.lifetime.Err() != nil {
		r.mu.Unlock()
		return nil, registry.ErrUnavailable
	}
	r.active++
	call, cancel := context.WithCancel(context.WithoutCancel(ctx))
	owner := r.lifetime
	r.mu.Unlock()
	stops := []func(){cancel}
	bind := func(owner context.Context) {
		if owner == nil {
			return
		}
		stop := context.AfterFunc(owner, cancel)
		stops = append(stops, func() { stop() })
		if owner.Err() != nil {
			cancel()
		}
	}
	bind(owner)
	var release func()
	finish := func() {
		for _, stop := range stops {
			stop()
		}
		if release != nil {
			release()
		}
		r.mu.Lock()
		r.active--
		if r.stopping && r.active == 0 {
			close(r.done)
		}
		r.mu.Unlock()
	}
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("subagent %q preparation panicked: %v", request.Name, failure)
		}
		if err != nil {
			finish()
			run = nil
		}
	}()
	mode := request.Mode
	var definition Subagent
	if request.Name != "" {
		var entry registry.Entry[Subagent]
		var leased context.Context
		entry, leased, release, err = r.store.Acquire(call, request.Name)
		if err != nil {
			return nil, err
		}
		call = leased
		definition = entry.Value
		if mode == "" {
			mode = definition.DefaultMode
		}
	}
	if mode == "" {
		mode = Async
	}
	if !validMode(mode) {
		return nil, fmt.Errorf("unknown subagent mode %q", mode)
	}
	if request.Timeout < 0 || (request.Timeout > 0 && mode != Sync) {
		return nil, fmt.Errorf("timeout requires sync mode and a positive duration")
	}
	if mode == Sync {
		bind(ctx)
	}
	bind(cfg.Lifetime)
	if request.Timeout > 0 {
		var timeoutCancel context.CancelFunc
		call, timeoutCancel = context.WithTimeout(call, request.Timeout)
		stops = append(stops, func() { timeoutCancel() })
	}
	if err := call.Err(); err != nil {
		return nil, err
	}
	label := strings.TrimSpace(request.Label)
	if label == "" {
		label = request.Name
	}
	if label == "" {
		label = labelFromPrompt(request.Input.Prompt)
	}
	cfg.AgentName = label
	task := request.Input.Prompt
	if request.Name != "" {
		cfg, task, err = definition.Prepare(call, cfg, request.Input)
		if err != nil {
			return nil, err
		}
	} else if request.Input.Payload != nil {
		return nil, fmt.Errorf("anonymous subagent requires text input")
	}
	if err := call.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(task) == "" {
		return nil, fmt.Errorf("subagent task is required")
	}
	detail := &types.DelegationDetail{Task: task, AgentName: label, AgentType: request.Name,
		RunMode: types.DelegationRunBackground, ContextMode: types.DelegationContextFresh}
	if mode == Sync {
		detail.RunMode = types.DelegationRunForeground
	}
	if mode == Fork {
		detail.ContextMode = types.DelegationContextFork
	}
	return &Run{Context: call, Config: cfg, Detail: detail, Mode: mode, cancel: cancel, finish: finish}, nil
}

// Execute is the session-free foreground entry point. Session adapters borrow
// Start for the same preparation and lease semantics.
func (r *Registry) Execute(ctx context.Context, cfg agent.Config, request Request) (*agent.Result, error) {
	if request.Mode != "" && request.Mode != Sync {
		return nil, fmt.Errorf("foreground execution requires sync mode")
	}
	request.Mode = Sync
	run, err := r.Start(ctx, cfg, request)
	if err != nil {
		return nil, err
	}
	defer run.Finish()
	return agent.RunTask(run.Context, run.Config, run.Detail)
}

func (r *Registry) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if !r.stopping {
		r.stopping = true
		if r.cancel != nil {
			r.cancel()
		}
		if r.active == 0 {
			close(r.done)
		}
	}
	r.mu.Unlock()
	if err := r.store.Close(ctx); err != nil {
		return err
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return errors.Join(resource.ErrCloseIncomplete, ctx.Err())
	}
}

func labelFromPrompt(prompt string) string {
	runes := []rune(strings.TrimSpace(prompt))
	if len(runes) > 30 {
		runes = runes[:30]
	}
	words := strings.Fields(string(runes))
	if len(words) > 4 {
		words = words[:4]
	}
	return strings.Join(words, "-")
}

var _ resource.Point[Subagent] = (*Registry)(nil)
var _ Executor = (*Registry)(nil)
