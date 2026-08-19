package commands

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	filepb "github.com/chainreactors/aiscan/aop/file"
	"github.com/chainreactors/aiscan/core/eventbus"
	coretool "github.com/chainreactors/aiscan/core/tool"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// FileAudit is the runtime's file-access audit trail: what the agent read, what
// it wrote, and what its shell commands left behind.
//
// An agent that operates on a machine is answerable for what it touched there,
// and a tool call alone does not say it. This type is where the tools in this
// package report that, and it publishes each observation as an aop.file.Access
// so the file namespace answers both "operate on this file" and "who touched
// it" — one mechanism rather than two.
//
// Recording is off the critical path by construction: it is non-blocking and
// lossy under pressure. A file access must never be slower because someone is
// watching, so a full queue drops the observation and counts it.
//
// Coverage is honest rather than complete. Tool-level records are exact. Shell
// executions are covered by diffing the work dir around them (Around), which
// sees every write but no read at all — an unmodified read leaves nothing
// behind to find. That difference is on the wire as AccessSource.
//
// A nil *FileAudit is a working no-op, so a runtime that never wired one up
// costs nothing and no call site needs a nil check.
type FileAudit struct {
	bus *eventbus.Bus[*filepb.Access]

	// prefix makes ids unique across processes, seq within one. Together they
	// let a consumer store an observation idempotently when a reconnect
	// redelivers it.
	prefix string
	seq    atomic.Uint64

	queue chan *filepb.Access
	done  chan struct{}
	stop  sync.Once

	mu      sync.RWMutex
	config  AuditOptions
	dropped atomic.Uint64
}

const (
	// auditQueueDepth is how many observations may wait for the publisher.
	// Deep enough that one snapshot diff of a busy build keeps its tail,
	// shallow enough that a stalled consumer cannot hold the whole listing.
	auditQueueDepth = 512

	// DefaultAuditMaxEntries bounds one snapshot walk. A work dir larger than
	// this is not diffed at all — see TakeSnapshot.
	DefaultAuditMaxEntries = 20000
)

// DefaultAuditIgnore are the path segments a snapshot never walks: the
// directories where a build or a checkout produces thousands of changes that
// say nothing about what the agent was doing.
var DefaultAuditIgnore = []string{".git", "node_modules", ".cairn", "__pycache__", ".venv"}

// AuditOptions is the snapshot and reporting policy. A peer sets it at runtime
// through the file namespace's Configure.
type AuditOptions struct {
	Enabled    bool
	Ignore     []string
	MaxEntries int
}

func defaultAuditOptions() AuditOptions {
	return AuditOptions{Enabled: true, Ignore: DefaultAuditIgnore, MaxEntries: DefaultAuditMaxEntries}
}

// NewFileAudit starts an audit trail. Nothing is published until something
// subscribes, and subscribers see every observation recorded after they attach.
func NewFileAudit() *FileAudit {
	a := &FileAudit{
		bus:    eventbus.New[*filepb.Access](),
		prefix: rand.Text()[:8],
		queue:  make(chan *filepb.Access, auditQueueDepth),
		done:   make(chan struct{}),
		config: defaultAuditOptions(),
	}
	go a.publish()
	return a
}

func (a *FileAudit) publish() {
	defer close(a.done)
	for access := range a.queue {
		a.bus.Emit(access)
	}
}

// Subscribe delivers every subsequent observation to handler, returning the
// detach function. Handlers run on the audit's own goroutine, never on the one
// that performed the file access.
func (a *FileAudit) Subscribe(handler func(*filepb.Access)) func() {
	if a == nil || handler == nil {
		return func() {}
	}
	return a.bus.Subscribe(handler)
}

// Configure applies a peer's watch policy. A nil config restores the defaults,
// which is what a peer that asked to observe with no opinion should get.
func (a *FileAudit) Configure(config *filepb.WatchConfig) {
	if a == nil {
		return
	}
	next := defaultAuditOptions()
	if config != nil {
		next.Enabled = config.GetEnabled()
		if ignore := config.GetIgnore(); len(ignore) > 0 {
			next.Ignore = ignore
		}
		if max := int(config.GetMaxEntries()); max > 0 {
			next.MaxEntries = max
		}
	}
	a.mu.Lock()
	a.config = next
	a.mu.Unlock()
}

// State is the reply a peer gets to Configure. Dropped rides along so a
// consumer can say the trail has a hole in it rather than presenting a short
// history as a complete one.
func (a *FileAudit) State() *filepb.WatchState {
	if a == nil {
		return &filepb.WatchState{}
	}
	state := &filepb.WatchState{Watching: a.Options().Enabled}
	if dropped := a.dropped.Load(); dropped > 0 {
		state.Error = fmt.Sprintf("%d observations dropped: consumer too slow", dropped)
	}
	return state
}

// Options returns the active policy. Callers about to do expensive work — a
// snapshot walk above all — check Enabled first.
func (a *FileAudit) Options() AuditOptions {
	if a == nil {
		return AuditOptions{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config
}

// Enabled reports whether observations are currently collected.
func (a *FileAudit) Enabled() bool { return a.Options().Enabled }

// Record publishes one observation. The caller supplies what it knows; identity,
// timing and the invocation context are filled in here so no call site has to
// remember them. It never blocks.
func (a *FileAudit) Record(ctx context.Context, access *filepb.Access) {
	if a == nil || access == nil || !a.Enabled() {
		return
	}
	invocation := coretool.InvocationFromContext(ctx)
	if access.ToolId == "" {
		access.ToolId = invocation.CallID
	}
	if access.WorkDir == "" {
		access.WorkDir = invocation.WorkDir
	}
	if access.Id == "" {
		access.Id = a.prefix + "-" + strconv.FormatUint(a.seq.Add(1), 36)
	}
	if access.Timestamp == nil {
		access.Timestamp = timestamppb.New(time.Now())
	}
	select {
	case a.queue <- access:
	default:
		a.dropped.Add(1)
	}
}

// RecordFile reports one exact, tool-level access. The size is read from the
// file unless the caller already knows it.
func (a *FileAudit) RecordFile(ctx context.Context, op filepb.AccessOp, path string, access *filepb.Access) {
	if a == nil || !a.Enabled() {
		return
	}
	if access == nil {
		access = &filepb.Access{}
	}
	access.Op = op
	access.Path = path
	if access.Source == filepb.AccessSource_ACCESS_SOURCE_UNSPECIFIED {
		access.Source = filepb.AccessSource_ACCESS_SOURCE_TOOL
	}
	if access.Size == 0 {
		if info, err := os.Stat(path); err == nil {
			access.Size = info.Size()
		}
	}
	a.Record(ctx, access)
}

// Close stops the publisher and waits for queued observations to drain.
func (a *FileAudit) Close() {
	if a == nil {
		return
	}
	a.stop.Do(func() {
		close(a.queue)
		<-a.done
	})
}

// AuditDigest is the content hash carried by a write observation. It is what
// lets a consumer tell a rewrite that changed nothing from one that changed
// everything, without storing either version.
func AuditDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// --- snapshots: the only way a shell command's writes become visible ---

// auditEntry is what a snapshot remembers about one file. Size and modification
// time together tell a rewrite from an untouched file, and cost one stat rather
// than a read of every byte in the work dir.
type auditEntry struct {
	modTime int64
	size    int64
}

// AuditSnapshot is a work dir's regular files at one moment, keyed by absolute
// path.
type AuditSnapshot map[string]auditEntry

// AuditChange is one difference between two snapshots.
type AuditChange struct {
	Path string
	Op   filepb.AccessOp
	Size int64
}

var errSnapshotTooLarge = fmt.Errorf("snapshot limit reached")

// TakeSnapshot walks root and records every regular file it is willing to look
// at.
//
// It returns an error rather than a partial listing when the tree exceeds
// MaxEntries: a diff against a truncated snapshot invents a deletion for every
// file that fell off the end, and a wrong audit trail is worse than an absent
// one. The caller reports the refusal instead.
func TakeSnapshot(root string, options AuditOptions) (AuditSnapshot, error) {
	if root == "" {
		return nil, fmt.Errorf("file audit: work dir is required")
	}
	max := options.MaxEntries
	if max <= 0 {
		max = DefaultAuditMaxEntries
	}
	ignore := options.Ignore
	if len(ignore) == 0 {
		ignore = DefaultAuditIgnore
	}

	snapshot := make(AuditSnapshot)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			// A permission hole somewhere in the tree must not cost the audit
			// of everything beside it.
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path != root && auditIgnored(entry.Name(), ignore) {
				return fs.SkipDir
			}
			return nil
		}
		// Sockets, devices and symlinks have no content this audit can speak
		// about, and following a link would double-count its target.
		if !entry.Type().IsRegular() || auditIgnored(entry.Name(), ignore) {
			return nil
		}
		if info, infoErr := entry.Info(); infoErr == nil {
			if len(snapshot) >= max {
				return errSnapshotTooLarge
			}
			snapshot[path] = auditEntry{modTime: info.ModTime().UnixNano(), size: info.Size()}
		}
		return nil
	})
	if err != nil {
		if err == errSnapshotTooLarge {
			return nil, fmt.Errorf("file audit: %s holds more than %d files", root, max)
		}
		return nil, err
	}
	return snapshot, nil
}

func auditIgnored(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern != "" && strings.Contains(name, pattern) {
			return true
		}
	}
	return false
}

// DiffSnapshots reports what happened to the work dir between two snapshots,
// sorted by path so the same pair always produces the same sequence.
//
// A file present in both is reported only when its size or modification time
// moved. That misses a rewrite that restored the previous bytes within the
// filesystem's timestamp resolution — the price of not hashing every file in
// the tree twice per command.
func DiffSnapshots(before, after AuditSnapshot) []AuditChange {
	var changes []AuditChange
	for path, now := range after {
		previous, existed := before[path]
		switch {
		case !existed:
			changes = append(changes, AuditChange{Path: path, Op: filepb.AccessOp_ACCESS_OP_CREATE, Size: now.size})
		case previous != now:
			changes = append(changes, AuditChange{Path: path, Op: filepb.AccessOp_ACCESS_OP_WRITE, Size: now.size})
		}
	}
	for path := range before {
		if _, survives := after[path]; !survives {
			changes = append(changes, AuditChange{Path: path, Op: filepb.AccessOp_ACCESS_OP_DELETE})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

// Around brackets fn with two snapshots of workDir and records the difference
// as SNAPSHOT observations attributed to the invocation in ctx.
//
// What it cannot do is separate the command's own writes from anything else
// that changed the work dir while it ran — a detached session, a background
// build — which is exactly what AccessSource SNAPSHOT tells a consumer.
func (a *FileAudit) Around(ctx context.Context, workDir string, fn func() error) error {
	if a == nil || workDir == "" || !a.Enabled() {
		return fn()
	}
	options := a.Options()
	before, err := TakeSnapshot(workDir, options)
	if err != nil {
		// Run the command regardless — auditing is never a reason not to do the
		// work — but say plainly that this execution has no file record.
		a.recordSnapshotError(ctx, workDir, err)
		return fn()
	}
	runErr := fn()
	after, err := TakeSnapshot(workDir, options)
	if err != nil {
		a.recordSnapshotError(ctx, workDir, err)
		return runErr
	}
	for _, change := range DiffSnapshots(before, after) {
		a.Record(ctx, &filepb.Access{
			Op:      change.Op,
			Source:  filepb.AccessSource_ACCESS_SOURCE_SNAPSHOT,
			Path:    change.Path,
			WorkDir: workDir,
			Size:    change.Size,
		})
	}
	return runErr
}

// recordSnapshotError reports that an execution went unaudited. It is an
// observation in its own right: a consumer that sees it knows the trail has a
// hole here, rather than reading an empty diff as "nothing was touched".
func (a *FileAudit) recordSnapshotError(ctx context.Context, workDir string, cause error) {
	a.Record(ctx, &filepb.Access{
		Source:  filepb.AccessSource_ACCESS_SOURCE_SNAPSHOT,
		Path:    workDir,
		WorkDir: workDir,
		Error:   cause.Error(),
	})
}
