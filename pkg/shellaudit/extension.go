// Package shellaudit observes process lifetimes and reports bounded directory
// snapshots using the existing file audit record format.
package shellaudit

import (
	"context"
	"errors"
	"fmt"
	"sync"

	filepb "github.com/chainreactors/aiscan/aop/file"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/fileaudit"
)

type Extension struct {
	mu       sync.Mutex
	bash     *commands.BashTool
	audit    *fileaudit.Audit
	auditSet *extension.Set
	sub      *commands.ProcessSubscription
	before   map[string]fileaudit.Snapshot
	closed   bool
}

func New(bash *commands.BashTool) (*Extension, error) {
	if bash == nil {
		return nil, fmt.Errorf("shell audit requires a terminal")
	}
	audit := fileaudit.New()
	set, err := extension.New(extension.Entry{ID: "audit", Extension: audit})
	if err != nil {
		return nil, err
	}
	return &Extension{bash: bash, audit: audit, auditSet: set, before: make(map[string]fileaudit.Snapshot)}, nil
}
func (m *Extension) Audit() *fileaudit.Audit { return m.audit }

func (m *Extension) Load(scope *extension.Context) error {
	ctx := scope.Init()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return commands.ErrUnavailable
	}
	if m.sub != nil {
		return nil
	}
	if err := m.auditSet.Load(ctx); err != nil {
		return err
	}
	sub, err := m.bash.ObserveProcesses(m.observe)
	if err != nil {
		return errors.Join(err, m.auditSet.Close(ctx))
	}
	m.sub = sub
	return nil
}

func (m *Extension) observe(ctx context.Context, id, directory string, started bool, _ error) {
	if started {
		if !m.audit.Enabled() {
			return
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return
		}
		m.before[id] = nil
		m.mu.Unlock()
		before, err := fileaudit.TakeSnapshot(directory, m.audit.Options())
		if err != nil {
			m.mu.Lock()
			delete(m.before, id)
			m.mu.Unlock()
			m.recordError(ctx, directory, err)
			return
		}
		m.mu.Lock()
		m.before[id] = before
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	before, ok := m.before[id]
	delete(m.before, id)
	m.mu.Unlock()
	if !ok {
		return
	}
	after, err := fileaudit.TakeSnapshot(directory, m.audit.Options())
	if err != nil {
		m.recordError(ctx, directory, err)
		return
	}
	for _, change := range fileaudit.DiffSnapshots(before, after) {
		m.audit.Record(ctx, &filepb.Access{Op: change.Op, Source: filepb.AccessSource_ACCESS_SOURCE_SNAPSHOT, Path: change.Path, WorkDir: directory, Size: change.Size})
	}
}

func (m *Extension) recordError(ctx context.Context, directory string, err error) {
	m.audit.Record(ctx, &filepb.Access{Source: filepb.AccessSource_ACCESS_SOURCE_SNAPSHOT, Path: directory, WorkDir: directory, Error: err.Error()})
}

func (m *Extension) Close(ctx context.Context) error {
	m.mu.Lock()
	// The terminal must finish its observations first. Keep the subscription
	// alive on a premature Close so a retry can receive the remaining exits.
	if len(m.before) != 0 {
		m.mu.Unlock()
		return fmt.Errorf("%w: terminal still has observed processes", extension.ErrCloseIncomplete)
	}
	m.closed = true
	sub := m.sub
	m.mu.Unlock()
	if sub != nil {
		if err := sub.Close(ctx); err != nil {
			return errors.Join(extension.ErrCloseIncomplete, err)
		}
	}
	return m.audit.Close(ctx)
}
