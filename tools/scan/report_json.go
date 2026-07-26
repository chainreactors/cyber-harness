package scan

import (
	"encoding/json"
	"strings"
)

func formatJSONLines(d *collector) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var sb strings.Builder
	for _, result := range d.gogoResults {
		line, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	for _, item := range d.sprayResults {
		line, err := json.Marshal(item.Result)
		if err != nil {
			return "", err
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}
