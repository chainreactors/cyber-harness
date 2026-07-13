package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

func (r *AgentConsole) completeAtReferenceFuzzy() bool {
	if r == nil || r.console == nil || r.console.Shell() == nil {
		return false
	}
	shell := r.console.Shell()
	line := []rune(string(*shell.Line()))
	start, end := tokenBounds(line, shell.Cursor().Pos())
	if start < 0 || end <= start || line[start] != '@' {
		return false
	}
	token := string(line[start:end])
	completed, ok := fuzzyAtFileCompletion(token)
	if !ok || completed == token {
		return false
	}
	next := append([]rune{}, line[:start]...)
	next = append(next, []rune(completed)...)
	next = append(next, line[end:]...)
	shell.Line().Set(next...)
	shell.Cursor().Set(start + len([]rune(completed)))
	shell.Refresh()
	return true
}

func tokenBounds(line []rune, cursor int) (int, int) {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(line) {
		cursor = len(line)
	}
	start := cursor
	for start > 0 && !unicode.IsSpace(line[start-1]) {
		start--
	}
	end := cursor
	for end < len(line) && !unicode.IsSpace(line[end]) {
		end++
	}
	return start, end
}

func fuzzyAtFileCompletion(token string) (string, bool) {
	raw := strings.TrimPrefix(token, "@")
	if raw == "" {
		return "", false
	}
	dirPart, query, sep := splitCompletionPath(raw)
	if query == "" {
		return "", false
	}
	dir := dirPart
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(filepath.FromSlash(dir))
	if err != nil {
		return "", false
	}
	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if fuzzySubsequence(query, name) {
			if entry.IsDir() {
				name += sep
			}
			matches = append(matches, "@"+dirPart+name)
		}
	}
	sort.Strings(matches)
	if len(matches) != 1 {
		return "", false
	}
	return matches[0], true
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
