package capability

// A Plan is the set of capabilities one binary should actually assemble. It
// replaces the four divergent BuildGroup lists that each entry point used to
// keep, so "which groups does this binary build" has exactly one answer.

type Options struct {
	// Groups limits the plan to these assembly groups. Nil means every linked
	// group. Entry points use this to describe their runtime surface without
	// keeping private factory lists.
	Groups []string
	// OptionalTools is --tools / config tools. Empty selects the defaults.
	OptionalTools []string
	// Extra force-enables capabilities that become available at runtime, such
	// as ioa once a client has connected.
	Extra []ID
}

type Plan struct {
	enabled map[ID]bool
	groups  []string
}

// Select resolves the registered descriptors against the caller's options.
// A capability that is not Optional is always part of the plan: it is linked,
// so it is meant to be there.
func Select(o Options) Plan {
	groups := map[string]bool{}
	for _, group := range o.Groups {
		groups[group] = true
	}
	chosen := map[string]bool{}
	for _, name := range o.OptionalTools {
		chosen[name] = true
	}
	extra := map[ID]bool{}
	for _, id := range o.Extra {
		extra[id] = true
	}

	p := Plan{enabled: map[ID]bool{}}
	seen := map[string]bool{}
	for _, d := range All() {
		if len(groups) > 0 && !groups[d.Group] {
			continue
		}
		switch {
		case extra[d.ID]:
		case !d.Optional:
		case len(chosen) > 0:
			if !chosen[string(d.ID)] && !chosen[d.Group] {
				continue
			}
		case !d.Default:
			continue
		}
		p.enabled[d.ID] = true
		if d.Group != "" && !seen[d.Group] {
			seen[d.Group] = true
			p.groups = append(p.groups, d.Group)
		}
	}
	return p
}

func (p Plan) Has(id ID) bool { return p.enabled[id] }

// Groups lists the factory groups to build, in registration order.
func (p Plan) Groups() []string { return append([]string(nil), p.groups...) }

func (p Plan) HasGroup(group string) bool {
	for _, g := range p.groups {
		if g == group {
			return true
		}
	}
	return false
}
