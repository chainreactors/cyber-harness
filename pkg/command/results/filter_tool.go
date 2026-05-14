package results

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chainreactors/parsers"
)

func filterGogoResults(lines []string, filter map[string]string, operator string, limit int) (string, error) {
	gogoOp := toGogoOperator(operator)
	var sb strings.Builder
	matched := 0
	total := 0

	for _, line := range lines {
		var r parsers.GOGOResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		total++
		if !matchGogoResult(&r, filter, gogoOp) {
			continue
		}
		matched++
		sb.WriteString(r.FullOutput())
		sb.WriteByte('\n')
		if matched >= limit {
			break
		}
	}

	header := fmt.Sprintf("Matched %d/%d results (showing %d):\n\n", matched, total, min(matched, limit))
	return truncateOutput(header + sb.String()), nil
}

func filterSprayResults(lines []string, filter map[string]string, operator string, limit int) (string, error) {
	var sb strings.Builder
	matched := 0
	total := 0

	for _, line := range lines {
		var r parsers.SprayResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		if r.ErrString != "" {
			continue
		}
		total++
		if !matchSprayResult(&r, filter, operator) {
			continue
		}
		matched++
		sb.WriteString(r.String())
		sb.WriteByte('\n')
		if matched >= limit {
			break
		}
	}

	header := fmt.Sprintf("Matched %d/%d results (showing %d):\n\n", matched, total, min(matched, limit))
	return truncateOutput(header + sb.String()), nil
}

func filterZombieResults(lines []string, filter map[string]string, operator string, limit int) (string, error) {
	var sb strings.Builder
	matched := 0
	total := 0

	for _, line := range lines {
		var r parsers.ZombieResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		total++
		if !matchZombieResult(&r, filter, operator) {
			continue
		}
		matched++
		sb.WriteString(r.Full())
		sb.WriteByte('\n')
		if matched >= limit {
			break
		}
	}

	header := fmt.Sprintf("Matched %d/%d results (showing %d):\n\n", matched, total, min(matched, limit))
	return truncateOutput(header + sb.String()), nil
}

func matchGogoResult(r *parsers.GOGOResult, filter map[string]string, gogoOp string) bool {
	for k, v := range filter {
		if !r.Filter(k, v, gogoOp) {
			return false
		}
	}
	return true
}

func matchSprayResult(r *parsers.SprayResult, filter map[string]string, operator string) bool {
	for k, v := range filter {
		fieldVal := r.Get(k)
		if !matchValue(fieldVal, v, operator) {
			return false
		}
	}
	return true
}

func matchZombieResult(r *parsers.ZombieResult, filter map[string]string, operator string) bool {
	for k, v := range filter {
		fieldVal := zombieFieldValue(r, k)
		if !matchValue(fieldVal, v, operator) {
			return false
		}
	}
	return true
}

func zombieFieldValue(r *parsers.ZombieResult, key string) string {
	switch strings.ToLower(key) {
	case "ip":
		return r.IP
	case "port":
		return r.Port
	case "service":
		return r.Service
	case "username", "user":
		return r.Username
	case "password", "pass":
		return r.Password
	case "scheme":
		return r.Scheme
	case "mod":
		return r.Mod.String()
	case "address", "addr":
		return r.Address()
	default:
		return ""
	}
}

func matchValue(fieldVal, matchVal, operator string) bool {
	fieldLower := strings.ToLower(fieldVal)
	matchLower := strings.ToLower(matchVal)
	switch operator {
	case "contains":
		return strings.Contains(fieldLower, matchLower)
	case "equals":
		return fieldLower == matchLower
	case "not_contains":
		return !strings.Contains(fieldLower, matchLower)
	case "not_equals":
		return fieldLower != matchLower
	default:
		return strings.Contains(fieldLower, matchLower)
	}
}

func toGogoOperator(operator string) string {
	switch operator {
	case "contains":
		return "::"
	case "equals":
		return "=="
	case "not_contains":
		return "!:"
	case "not_equals":
		return "!="
	default:
		return "::"
	}
}
