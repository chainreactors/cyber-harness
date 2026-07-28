package aop

import (
	"encoding/json"
	"fmt"
)

// DecodeData decodes an event payload without changing or replacing the
// original envelope.
func DecodeData[T any](event Event) (T, error) {
	var data T
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return data, fmt.Errorf("decode AOP %s data: %w", event.Type, err)
	}
	return data, nil
}
