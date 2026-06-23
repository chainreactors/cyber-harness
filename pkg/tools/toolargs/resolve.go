package toolargs

import (
	"path/filepath"
	"strings"
)

// ResolveRelativePaths resolves relative file paths in args against workDir.
// fileFlags is the set of flags whose next argument (or =value) is a file path.
func ResolveRelativePaths(args []string, fileFlags map[string]bool, workDir string) []string {
	if workDir == "" {
		return args
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if key, value, ok := strings.Cut(arg, "="); ok {
			if fileFlags[key] {
				out = append(out, key+"="+ResolvePath(value, workDir))
				continue
			}
			out = append(out, arg)
			continue
		}
		if fileFlags[arg] && i+1 < len(args) {
			out = append(out, arg)
			i++
			out = append(out, ResolvePath(args[i], workDir))
			continue
		}
		out = append(out, arg)
	}
	return out
}

// ResolvePath resolves a single path relative to workDir.
// Returns value unchanged if empty, absolute, or starts with "-".
func ResolvePath(value, workDir string) string {
	if value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "-") {
		return value
	}
	return filepath.Join(workDir, value)
}
