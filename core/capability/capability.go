// Package capability describes the features of one explicit product edition.
// A Catalog is immutable after construction. Importing a tool package never
// changes process-wide selection, help, or skill visibility.
package capability

import (
	"fmt"
	"sort"
	"strings"
)

type ID string

type Kind uint8

const (
	KindTool Kind = iota
	KindScanner
	KindService
)

type Descriptor struct {
	ID        ID
	Kind      Kind
	Group     string
	CLIName   string
	Summary   string
	UsageLine string
	Usage     func() string
	Skills    []string
	Optional  bool
	Default   bool
}

// Catalog is the linked feature surface of one edition. It contains metadata
// only; extension.Entry remains the authority for instantiated modules.
type Catalog struct {
	order []Descriptor
	byID  map[ID]int
}

func New(descriptors ...Descriptor) (Catalog, error) {
	result := Catalog{order: make([]Descriptor, 0, len(descriptors)), byID: make(map[ID]int, len(descriptors))}
	cli := make(map[string]ID)
	for _, descriptor := range descriptors {
		if strings.TrimSpace(string(descriptor.ID)) == "" {
			return Catalog{}, fmt.Errorf("capability ID is required")
		}
		if _, exists := result.byID[descriptor.ID]; exists {
			return Catalog{}, fmt.Errorf("duplicate capability %s", descriptor.ID)
		}
		if descriptor.Group == "" {
			descriptor.Group = string(descriptor.ID)
		}
		if descriptor.CLIName != "" {
			if owner, exists := cli[descriptor.CLIName]; exists {
				return Catalog{}, fmt.Errorf("duplicate capability command %s in %s and %s", descriptor.CLIName, owner, descriptor.ID)
			}
			cli[descriptor.CLIName] = descriptor.ID
		}
		descriptor.Skills = append([]string(nil), descriptor.Skills...)
		result.byID[descriptor.ID] = len(result.order)
		result.order = append(result.order, descriptor)
	}
	return result, nil
}

func Must(descriptors ...Descriptor) Catalog {
	catalog, err := New(descriptors...)
	if err != nil {
		panic(err)
	}
	return catalog
}

func (c Catalog) All() []Descriptor {
	result := make([]Descriptor, len(c.order))
	copy(result, c.order)
	for i := range result {
		result[i].Skills = append([]string(nil), result[i].Skills...)
	}
	return result
}

func (c Catalog) Get(id ID) (Descriptor, bool) {
	index, ok := c.byID[id]
	if !ok || index < 0 || index >= len(c.order) {
		return Descriptor{}, false
	}
	descriptor := c.order[index]
	descriptor.Skills = append([]string(nil), descriptor.Skills...)
	return descriptor, true
}

func (c Catalog) Enabled(id ID) bool {
	_, ok := c.byID[id]
	return ok
}

func (c Catalog) Groups() []string {
	seen := make(map[string]bool)
	var result []string
	for _, descriptor := range c.order {
		if descriptor.Group != "" && !seen[descriptor.Group] {
			seen[descriptor.Group] = true
			result = append(result, descriptor.Group)
		}
	}
	return result
}

func (c Catalog) IDsSorted() []string {
	ids := make([]string, 0, len(c.order))
	for _, descriptor := range c.order {
		ids = append(ids, string(descriptor.ID))
	}
	sort.Strings(ids)
	return ids
}

func (c Catalog) Empty() bool { return len(c.order) == 0 }
