package observe

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	filepb "github.com/chainreactors/aiscan/aop/file"
)

const DefaultMaxEntries = 20000

var DefaultIgnore = []string{".git", "node_modules", ".cairn", "__pycache__", ".venv"}

type FileOptions struct {
	Enabled    bool
	Ignore     []string
	MaxEntries int
}

func defaultFileOptions() FileOptions {
	return FileOptions{Enabled: true, Ignore: append([]string(nil), DefaultIgnore...), MaxEntries: DefaultMaxEntries}
}

type snapshotEntry struct {
	modTime int64
	size    int64
}

type Snapshot map[string]snapshotEntry

type Change struct {
	Path string
	Op   filepb.AccessOp
	Size int64
}

var errSnapshotTooLarge = fmt.Errorf("snapshot limit reached")

func TakeSnapshot(root string, options FileOptions) (Snapshot, error) {
	if root == "" {
		return nil, fmt.Errorf("file observation: work directory is required")
	}
	maxEntries := options.MaxEntries
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	ignore := options.Ignore
	if len(ignore) == 0 {
		ignore = DefaultIgnore
	}
	result := make(Snapshot)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if path != root && ignored(entry.Name(), ignore) {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || ignored(entry.Name(), ignore) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if len(result) >= maxEntries {
			return errSnapshotTooLarge
		}
		result[path] = snapshotEntry{modTime: info.ModTime().UnixNano(), size: info.Size()}
		return nil
	})
	if err == errSnapshotTooLarge {
		return nil, fmt.Errorf("file observation: %s holds more than %d files", root, maxEntries)
	}
	return result, err
}

func ignored(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern != "" && strings.Contains(name, pattern) {
			return true
		}
	}
	return false
}

func DiffSnapshots(before, after Snapshot) []Change {
	var changes []Change
	for path, now := range after {
		previous, existed := before[path]
		switch {
		case !existed:
			changes = append(changes, Change{Path: path, Op: filepb.AccessOp_ACCESS_OP_CREATE, Size: now.size})
		case previous != now:
			changes = append(changes, Change{Path: path, Op: filepb.AccessOp_ACCESS_OP_WRITE, Size: now.size})
		}
	}
	for path := range before {
		if _, exists := after[path]; !exists {
			changes = append(changes, Change{Path: path, Op: filepb.AccessOp_ACCESS_OP_DELETE})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}
