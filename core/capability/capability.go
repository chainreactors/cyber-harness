// Package capability is the single answer to "is this feature part of this
// binary". A capability exists if and only if its package is linked, and it is
// linked if and only if a blank import in cmd/*/imports*.go pulls it in — so
// build tags and blank imports stay the edition switch, and everything else
// (CLI help, scanner availability, skill gating, tool-group assembly) is
// derived from the descriptors registered here instead of from parallel global
// tables.
//
// The package deliberately imports nothing from aiscan so that core/config,
// skills, pkg/commands and pkg/tools can all depend on it.
package capability

import (
	"sort"
	"sync"
)

type ID string

type Kind uint8

const (
	KindTool    Kind = iota // agent tool group
	KindScanner             // CLI-facing scanner command
	KindService             // ioa / proxy / web
)

// Descriptor is what a capability package declares about itself in init().
type Descriptor struct {
	ID   ID
	Kind Kind
	// Group is the command-factory group; empty means the ID is the group.
	Group string
	// CLIName is the top-level command name; empty means not CLI-facing.
	CLIName string
	// Summary is the word shown in the CLI command summary line.
	Summary string
	// UsageLine is the pre-aligned row shown in the scanner usage block.
	UsageLine string
	// Usage renders the command's full help lazily, so registering a
	// capability never costs the work of building its usage text.
	Usage func() string
	// Skills lists skill names this capability unlocks.
	Skills []string
	// Optional marks a group selectable through --tools.
	Optional bool
	// Default enables an Optional group when --tools is empty.
	Default bool
	// Requires names the dependencies the factory needs, for the skip log.
	Requires []string
}

// Conflict records a duplicate registration. First registration wins; the
// duplicate is reported once at startup rather than silently shadowing.
type Conflict struct {
	ID    ID
	Group string
}

var (
	mu        sync.RWMutex
	order     []ID
	byID      = map[ID]Descriptor{}
	conflicts []Conflict
)

// Register declares a capability. Called from init(); first registration wins.
func Register(d Descriptor) {
	if d.ID == "" {
		return
	}
	if d.Group == "" {
		d.Group = string(d.ID)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, exists := byID[d.ID]; exists {
		conflicts = append(conflicts, Conflict{ID: d.ID, Group: d.Group})
		return
	}
	byID[d.ID] = d
	order = append(order, d.ID)
}

// All returns the descriptors in registration order.
func All() []Descriptor {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Descriptor, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func Get(id ID) (Descriptor, bool) {
	mu.RLock()
	defer mu.RUnlock()
	d, ok := byID[id]
	return d, ok
}

// Enabled reports whether the capability is linked into this binary.
func Enabled(id ID) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := byID[id]
	return ok
}

func Conflicts() []Conflict {
	mu.RLock()
	defer mu.RUnlock()
	return append([]Conflict(nil), conflicts...)
}

// Groups lists every distinct factory group, in registration order.
func Groups() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range All() {
		if d.Group == "" || seen[d.Group] {
			continue
		}
		seen[d.Group] = true
		out = append(out, d.Group)
	}
	return out
}

// IDsSorted is the stable identity of an edition, for golden tests.
func IDsSorted() []string {
	ids := make([]string, 0, len(All()))
	for _, d := range All() {
		ids = append(ids, string(d.ID))
	}
	sort.Strings(ids)
	return ids
}

// reset clears the registry. Tests only.
func reset() {
	mu.Lock()
	defer mu.Unlock()
	order = nil
	byID = map[ID]Descriptor{}
	conflicts = nil
}
