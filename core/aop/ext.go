package aop

import (
	"encoding/json"
	"fmt"
)

// Ext decodes one extension namespace without touching the others.
//
// Ext/SetExt are the codec primitives for the extension map. Business code
// must not call them directly — use the typed namespace packages under
// core/aop/x/<ns> (or pkg/webproto for hub-owned namespaces) instead.
func Ext[T any](event Event, namespace string) (T, bool, error) {
	var value T
	raw, ok := event.Ext[namespace]
	if !ok {
		return value, false, nil
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, true, fmt.Errorf("decode AOP ext.%s: %w", namespace, err)
	}
	return value, true, nil
}

// SetExt serializes one namespace while preserving all other raw namespaces.
func SetExt[T any](event *Event, namespace string, value T) error {
	if event == nil {
		return fmt.Errorf("set AOP ext.%s on nil event", namespace)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode AOP ext.%s: %w", namespace, err)
	}
	if event.Ext == nil {
		event.Ext = make(map[string]json.RawMessage)
	}
	event.Ext[namespace] = raw
	return nil
}
