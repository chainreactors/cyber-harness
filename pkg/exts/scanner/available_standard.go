//go:build !full

package scanner

func editionNames() []string             { return nil }
func editionUsage(string) (string, bool) { return "", false }
func editionDescription(string) string   { return "" }
