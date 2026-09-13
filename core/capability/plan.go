package capability

type Options struct {
	Groups        []string
	OptionalTools []string
	Extra         []ID
}

type Plan struct {
	enabled map[ID]bool
	groups  []string
}

func (c Catalog) Select(options Options) Plan {
	groups := make(map[string]bool)
	for _, group := range options.Groups {
		groups[group] = true
	}
	chosen := make(map[string]bool)
	for _, name := range options.OptionalTools {
		chosen[name] = true
	}
	extra := make(map[ID]bool)
	for _, id := range options.Extra {
		extra[id] = true
	}
	plan := Plan{enabled: make(map[ID]bool)}
	seen := make(map[string]bool)
	for _, descriptor := range c.order {
		if len(groups) > 0 && !groups[descriptor.Group] {
			continue
		}
		switch {
		case extra[descriptor.ID]:
		case !descriptor.Optional:
		case len(chosen) > 0:
			if !chosen[string(descriptor.ID)] && !chosen[descriptor.Group] {
				continue
			}
		case !descriptor.Default:
			continue
		}
		plan.enabled[descriptor.ID] = true
		if descriptor.Group != "" && !seen[descriptor.Group] {
			seen[descriptor.Group] = true
			plan.groups = append(plan.groups, descriptor.Group)
		}
	}
	return plan
}

func (p Plan) Has(id ID) bool   { return p.enabled[id] }
func (p Plan) Groups() []string { return append([]string(nil), p.groups...) }
func (p Plan) HasGroup(group string) bool {
	for _, current := range p.groups {
		if current == group {
			return true
		}
	}
	return false
}
