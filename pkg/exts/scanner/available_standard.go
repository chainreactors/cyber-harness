//go:build !full

package scanner

func manifestNames() []string             { return nil }
func manifestUsage(string) (string, bool) { return "", false }
func manifestDescription(string) string   { return "" }
