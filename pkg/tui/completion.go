package tui

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/carapace-sh/carapace"
	"github.com/chainreactors/tui/readline"
)

// wrapCompleterForFuzzyAt wraps the shell's Completer so that when the word
// under the cursor starts with '@', the PREFIX is shortened to just '@'. This
// prevents readline's FilterPrefix from discarding fuzzy (non-prefix) matches
// produced by atFuzzyFileAction.
func wrapCompleterForFuzzyAt(shell *readline.Shell) {
	if shell == nil || shell.Completer == nil {
		return
	}
	inner := shell.Completer
	shell.Completer = func(line []rune, cursor int) readline.Completions {
		comps := inner(line, cursor)
		if wordAtCursorIsAtRef(line, cursor) {
			comps.PREFIX = "@"
		}
		return comps
	}
}

func wordAtCursorIsAtRef(line []rune, cursor int) bool {
	if cursor > len(line) {
		cursor = len(line)
	}
	start := cursor
	for start > 0 && !unicode.IsSpace(line[start-1]) {
		start--
	}
	return start < cursor && line[start] == '@'
}

func atFuzzyFileAction(raw string) carapace.Action {
	return carapace.ActionCallback(func(_ carapace.Context) carapace.Action {
		dirPart, query, sep := splitCompletionPath(raw)
		dir := dirPart
		if dir == "" {
			dir = "."
		}
		entries, err := os.ReadDir(filepath.FromSlash(dir))
		if err != nil {
			return carapace.ActionValues()
		}
		var values []string
		for _, entry := range entries {
			name := entry.Name()
			if query == "" || fuzzySubsequence(query, name) {
				if entry.IsDir() {
					name += sep
				}
				values = append(values, "@"+dirPart+name)
			}
		}
		return carapace.ActionValues(values...).NoSpace()
	})
}

func splitCompletionPath(raw string) (dir, query, sep string) {
	sep = string(os.PathSeparator)
	if strings.Contains(raw, "/") {
		sep = "/"
	}
	if strings.Contains(raw, "\\") {
		sep = "\\"
	}
	idx := strings.LastIndexAny(raw, `/\`)
	if idx < 0 {
		return "", raw, sep
	}
	return raw[:idx+1], raw[idx+1:], sep
}

func fuzzySubsequence(query, value string) bool {
	query = strings.ToLower(query)
	value = strings.ToLower(value)
	j := 0
	for _, r := range value {
		if j < len(query) && rune(query[j]) == r {
			j++
		}
	}
	return j == len(query)
}
