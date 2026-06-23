package toolargs

import "strings"

// CommonAliases defines nuclei-style short flag aliases shared across tools.
var CommonAliases = map[string]string{
	"-etags": "-exclude-tags",
	"-eid":   "-exclude-id",
	"-es":    "-exclude-severity",
	"-tl":    "-template-list",
}

// NormalizeFlags converts single-dash flags to double-dash when they match
// a known flag, and applies alias mappings. Unknown flags pass through.
func NormalizeFlags(args []string, known map[string]struct{}, aliases map[string]string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		key := arg
		suffix := ""
		if i := strings.IndexByte(arg, '='); i > 0 {
			key = arg[:i]
			suffix = arg[i:]
		}
		if replacement, ok := aliases[key]; ok {
			key = replacement
		}
		if _, ok := known[key]; ok {
			out = append(out, "-"+key+suffix)
		} else {
			out = append(out, arg)
		}
	}
	return out
}
