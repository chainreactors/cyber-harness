package config

// Flat presents registered extension sections at the document root. The
// extensions envelope remains available for old files and the wire protocol;
// unknown sections keep that envelope so other hosts can preserve them.
func (r *Sections) Flat(document map[string]any) map[string]any {
	out := CloneDocument(document)
	if r == nil {
		return out
	}
	extensions, _ := out["extensions"].(map[string]any)
	if values, ok := out["extensions"].(Values); ok {
		extensions = make(map[string]any, len(values))
		for key, fields := range values {
			extensions[key] = fields
		}
		out["extensions"] = extensions
	}
	for key, fields := range extensions {
		if r.Has(key) {
			if root, ok := out[key].(map[string]any); ok {
				nested, _ := fields.(map[string]any)
				mergeFields(root, nested)
			} else {
				out[key] = fields
			}
			delete(extensions, key)
		}
	}
	if len(extensions) == 0 {
		delete(out, "extensions")
	}
	return out
}
