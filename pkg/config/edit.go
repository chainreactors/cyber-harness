package config

import (
	"fmt"
	"reflect"
)

// ApplyChanges persists only edits relative to the merged file view. Inherited
// values never become local merely because an editor sends its complete form.
func (s *Snapshot) ApplyChanges(before, after map[string]any) (map[string]any, error) {
	target := s.TargetDocument()
	// An overridden inherited profile cannot be deleted by removing its local entry.
	nextLLM, _ := after["llm"].(map[string]any)
	nextProfiles, _ := nextLLM["providers"].([]any)
	if len(nextProfiles) > 0 {
		wanted := map[string]bool{}
		for _, raw := range nextProfiles {
			p, _ := raw.(map[string]any)
			id, _ := p["id"].(string)
			wanted[id] = true
		}
		oldLLM, _ := before["llm"].(map[string]any)
		oldProfiles, _ := oldLLM["providers"].([]any)
		removed := map[string]bool{}
		for _, raw := range oldProfiles {
			p, _ := raw.(map[string]any)
			id, _ := p["id"].(string)
			if !wanted[id] {
				removed[id] = true
			}
		}
		for _, layer := range s.Layers {
			if samePath(layer.Path, s.Target) {
				continue
			}
			doc := CloneDocument(layer.Document)
			if err := normalizeProfileDocument(doc); err != nil {
				return nil, err
			}
			llm, _ := doc["llm"].(map[string]any)
			profiles, _ := llm["providers"].([]any)
			for _, raw := range profiles {
				p, _ := raw.(map[string]any)
				id, _ := p["id"].(string)
				if removed[id] {
					return nil, fmt.Errorf("profile %q is inherited; edit its source configuration to remove it", id)
				}
			}
		}
	}
	out := CloneDocument(target)
	if err := normalizeProfileDocument(out); err != nil {
		return nil, err
	}
	if err := applyChanges(out, before, after, ""); err != nil {
		return nil, err
	}
	return out, nil
}

func applyChanges(target, before, after map[string]any, prefix string) error {
	for key, next := range after {
		previous := before[key]
		if reflect.DeepEqual(previous, next) {
			continue
		}
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if path == "llm.providers" {
			list, _ := next.([]any)
			old, _ := previous.([]any)
			local, _ := target[key].([]any)
			if len(list) == 0 {
				target[key] = []any{}
				continue
			}
			byID := func(items []any) map[string]map[string]any {
				out := map[string]map[string]any{}
				for _, v := range items {
					if m, ok := v.(map[string]any); ok {
						if id, ok := m["id"].(string); ok {
							out[id] = m
						}
					}
				}
				return out
			}
			oldIDs, newIDs, localIDs := byID(old), byID(list), byID(local)
			for id := range oldIDs {
				if _, ok := newIDs[id]; !ok {
					if _, local := localIDs[id]; !local {
						return fmt.Errorf("profile %q is inherited; edit its source configuration to remove it", id)
					}
					delete(localIDs, id)
				}
			}
			for _, v := range list {
				m := v.(map[string]any)
				id, _ := m["id"].(string)
				if reflect.DeepEqual(m, oldIDs[id]) {
					continue
				}
				entry := localIDs[id]
				if entry == nil {
					entry = map[string]any{"id": id}
					localIDs[id] = entry
				}
				if err := applyChanges(entry, oldIDs[id], m, path+"."+id); err != nil {
					return err
				}
			}
			result := []any{}
			for _, v := range list {
				id, _ := v.(map[string]any)["id"].(string)
				if m, ok := localIDs[id]; ok {
					result = append(result, m)
				}
			}
			if len(result) > 0 {
				target[key] = result
			} else {
				delete(target, key)
			}
			continue
		}
		if nested, ok := next.(map[string]any); ok {
			prior, _ := previous.(map[string]any)
			local, _ := target[key].(map[string]any)
			if local == nil {
				local = map[string]any{}
			}
			if err := applyChanges(local, prior, nested, path); err != nil {
				return err
			}
			if len(local) > 0 {
				target[key] = local
			}
			continue
		}
		target[key] = next
	}
	for key := range before {
		if _, ok := after[key]; !ok {
			delete(target, key)
		}
	}
	return nil
}

// TargetDocument returns an owned copy of the selected writable layer.
func (s *Snapshot) TargetDocument() map[string]any {
	for _, layer := range s.Layers {
		if samePath(layer.Path, s.Target) {
			return CloneDocument(layer.Document)
		}
	}
	return map[string]any{}
}
