package capability

func (c Catalog) CLIAvailable(name string) bool { _, ok := c.byCLIName(name); return ok }
func (c Catalog) UsageLines() []string {
	var result []string
	for _, descriptor := range c.order {
		if descriptor.CLIName != "" && descriptor.UsageLine != "" {
			result = append(result, descriptor.UsageLine)
		}
	}
	return result
}
func (c Catalog) Summaries() []string {
	var result []string
	for _, descriptor := range c.order {
		if descriptor.CLIName != "" && descriptor.Summary != "" {
			result = append(result, descriptor.Summary)
		}
	}
	return result
}
func (c Catalog) Usage(name string) (string, bool) {
	descriptor, ok := c.byCLIName(name)
	if !ok || descriptor.Usage == nil {
		return "", false
	}
	return descriptor.Usage(), true
}
func (c Catalog) byCLIName(name string) (Descriptor, bool) {
	if name == "" {
		return Descriptor{}, false
	}
	for _, descriptor := range c.order {
		if descriptor.CLIName == name {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}
