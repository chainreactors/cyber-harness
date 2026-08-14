package toolargs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// A control plane delivers one artifact event as one frame and bounds a frame:
// past its limit the frame is dropped, and past a hard limit the transport
// closes the connection outright -- taking every other tool call sharing it down
// with it. A scanner-native record is the one artifact with no natural bound: a
// single katana crawl result carries the response body next to the raw
// transcript of that same response, and katana's own body ceiling alone is 4
// MiB, so one record can reach eight to ten.
//
// EmitArtifactResultCtx is the sole place a scanner artifact is built, and the
// record is still structured JSON there. Oversized records are therefore
// encoded under a hard budget at that boundary. Projection fields are emitted
// first, body/transcript fields last, and each child value receives its own
// budget so one collection cannot crowd every useful field out of the record.
const (
	maxArtifactDataBytes           = 4 << 20
	maxArtifactStringBytes         = 64 << 10
	maxArtifactProjectedValueBytes = 512 << 10
	maxArtifactOtherValueBytes     = 256 << 10
	maxArtifactJSONDepth           = 128
	artifactBudgetMetadataField    = "_aiscan_artifact"
)

// boundArtifactData returns data byte-for-byte when it is within budget. An
// oversized record is decoded with UseNumber, then deterministically re-encoded
// under maxArtifactDataBytes. The result is always non-nil, valid JSON, shorter
// than the oversized input, and no larger than the hard budget.
func boundArtifactData(data []byte) []byte {
	if len(data) <= maxArtifactDataBytes {
		return data
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var record any
	if err := decoder.Decode(&record); err != nil {
		return artifactBudgetFallback(len(data))
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return artifactBudgetFallback(len(data))
	}

	bounded, ok := marshalBoundedArtifactValue(
		markArtifactTruncated(record, len(data)),
		maxArtifactDataBytes,
		0,
	)
	if !ok || len(bounded) > maxArtifactDataBytes || !json.Valid(bounded) {
		return artifactBudgetFallback(len(data))
	}
	return bounded
}

func markArtifactTruncated(record any, originalBytes int) any {
	metadata := map[string]any{
		"limit_bytes":    maxArtifactDataBytes,
		"original_bytes": originalBytes,
		"truncated":      true,
	}
	if fields, ok := record.(map[string]any); ok {
		marked := make(map[string]any, len(fields)+1)
		for name, value := range fields {
			marked[name] = value
		}
		marked[artifactBudgetMetadataField] = metadata
		return marked
	}
	return map[string]any{
		artifactBudgetMetadataField: metadata,
		"value":                     record,
	}
}

func artifactBudgetFallback(originalBytes int) []byte {
	data, _ := json.Marshal(map[string]any{
		artifactBudgetMetadataField: map[string]any{
			"limit_bytes":     maxArtifactDataBytes,
			"original_bytes":  originalBytes,
			"payload_omitted": true,
			"truncated":       true,
		},
	})
	return data
}

// marshalBoundedArtifactValue emits one complete JSON value within limit. Maps
// sort projection keys ahead of ordinary data and bulk fields; arrays retain
// their original order. A root-level metadata field records that some of the
// original representation was omitted.
func marshalBoundedArtifactValue(value any, limit, depth int) ([]byte, bool) {
	if limit <= 0 {
		return nil, false
	}
	if depth >= maxArtifactJSONDepth {
		if limit < len("null") {
			return nil, false
		}
		return []byte("null"), true
	}

	switch typed := value.(type) {
	case nil:
		if limit < len("null") {
			return nil, false
		}
		return []byte("null"), true
	case bool:
		raw, _ := json.Marshal(typed)
		if len(raw) > limit {
			return nil, false
		}
		return raw, true
	case json.Number:
		raw := []byte(typed.String())
		if len(raw) > limit {
			return nil, false
		}
		return raw, true
	case string:
		return marshalBoundedArtifactString(typed, min(limit, maxArtifactStringBytes))
	case []any:
		return marshalBoundedArtifactArray(typed, limit, depth+1)
	case map[string]any:
		return marshalBoundedArtifactMap(typed, limit, depth+1)
	default:
		raw, err := json.Marshal(typed)
		if err != nil || len(raw) > limit {
			return nil, false
		}
		return raw, true
	}
}

func marshalBoundedArtifactString(value string, limit int) ([]byte, bool) {
	if limit < 2 {
		return nil, false
	}
	// JSON string encoding never uses fewer bytes than the UTF-8 input plus its
	// quotes. Avoid copying a multi-megabyte body merely to prove that it cannot
	// fit a 64 KiB value budget.
	if len(value)+2 <= limit {
		raw, _ := json.Marshal(value)
		if len(raw) <= limit {
			return raw, true
		}
	}

	marker := fmt.Sprintf("…[aiscan: truncated, %d bytes total]", len(value))
	markerRaw, _ := json.Marshal(marker)
	if len(markerRaw) > limit {
		return []byte(`""`), true
	}

	// JSON escaping means an N-byte prefix does not necessarily occupy N bytes
	// on the wire. Find the largest UTF-8-safe prefix whose encoded form plus the
	// complete marker stays inside the value budget.
	low := 0
	high := min(len(value), limit-len(markerRaw)+2)
	best := markerRaw
	for low <= high {
		middle := low + (high-low)/2
		prefix := strings.ToValidUTF8(value[:middle], "")
		candidate, _ := json.Marshal(prefix + marker)
		if len(candidate) <= limit {
			best = candidate
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	return best, true
}

func marshalBoundedArtifactArray(items []any, limit, depth int) ([]byte, bool) {
	if limit < 2 {
		return nil, false
	}
	out := make([]byte, 0, min(limit, 1024))
	out = append(out, '[')
	written := 0
	for _, item := range items {
		separatorBytes := 0
		if written > 0 {
			separatorBytes = 1
		}
		remaining := limit - len(out) - separatorBytes - 1 // closing ']'
		if remaining <= 0 {
			break
		}
		itemBudget := min(remaining, maxArtifactOtherValueBytes)
		encoded, ok := marshalBoundedArtifactValue(item, itemBudget, depth)
		if !ok {
			continue
		}
		if written > 0 {
			out = append(out, ',')
		}
		out = append(out, encoded...)
		written++
	}
	out = append(out, ']')
	return out, true
}

func marshalBoundedArtifactMap(fields map[string]any, limit, depth int) ([]byte, bool) {
	if limit < 2 {
		return nil, false
	}
	keys := make([]string, 0, len(fields))
	for name := range fields {
		keys = append(keys, name)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := artifactFieldPriority(keys[i], fields[keys[i]])
		right := artifactFieldPriority(keys[j], fields[keys[j]])
		if left != right {
			return left < right
		}
		return keys[i] < keys[j]
	})

	out := make([]byte, 0, min(limit, 1024))
	out = append(out, '{')
	written := 0
	for _, name := range keys {
		if limit-len(out)-1 < len("null") {
			break
		}
		// Escaping can only make a JSON object key larger. Skip an impossible key
		// without allocating a second copy of a peer-controlled multi-megabyte name.
		if len(name)+2 > limit-len(out)-2 {
			continue
		}
		encodedName, _ := json.Marshal(name)
		separatorBytes := 0
		if written > 0 {
			separatorBytes = 1
		}
		entryBytes := separatorBytes + len(encodedName) + 1 // key + ':'
		remaining := limit - len(out) - entryBytes - 1      // closing '}'
		if remaining <= 0 {
			continue
		}
		valueBudget := min(remaining, artifactFieldValueBudget(name, fields[name]))
		encodedValue, ok := marshalBoundedArtifactValue(fields[name], valueBudget, depth)
		if !ok {
			continue
		}
		if written > 0 {
			out = append(out, ',')
		}
		out = append(out, encodedName...)
		out = append(out, ':')
		out = append(out, encodedValue...)
		written++
	}
	out = append(out, '}')
	return out, true
}

func artifactFieldPriority(name string, value any) int {
	if name == artifactBudgetMetadataField {
		return 0
	}
	if isArtifactProjectionField(name) || isArtifactProjectionContainer(name, value) {
		return 1
	}
	if !isArtifactBulkField(name) && isArtifactScalar(value) {
		return 2
	}
	if isArtifactBulkField(name) {
		return 4
	}
	return 3
}

func artifactFieldValueBudget(name string, value any) int {
	if name == artifactBudgetMetadataField {
		return 4 << 10
	}
	if isArtifactProjectionField(name) || isArtifactProjectionContainer(name, value) {
		return maxArtifactProjectedValueBytes
	}
	if isArtifactBulkField(name) {
		return maxArtifactStringBytes
	}
	return maxArtifactOtherValueBytes
}

func isArtifactProjectionContainer(name string, value any) bool {
	name = strings.ToLower(name)
	if name != "request" && name != "response" {
		return false
	}
	switch value.(type) {
	case []any, map[string]any:
		return true
	default:
		return false
	}
}

func isArtifactScalar(value any) bool {
	switch value.(type) {
	case nil, bool, json.Number, string:
		return true
	default:
		return false
	}
}

func isArtifactBulkField(name string) bool {
	switch strings.ToLower(name) {
	case "body", "content", "data", "header", "payload", "raw",
		"request", "request_body", "request_raw",
		"response", "response_body", "response_raw":
		return true
	default:
		return false
	}
}

// These are the scalar fields and small collections consumed by scanner-to-
// graph projections. Keeping the list at the artifact boundary makes the
// fallback deterministic without coupling the transport to one concrete Go
// result type.
func isArtifactProjectionField(name string) bool {
	switch strings.ToLower(name) {
	case "app_id", "asset_id", "attributes", "body_length", "content_length",
		"content_type", "cstx_id", "cstx_type", "endpoint", "error",
		"extracted", "extracts", "fingerprint", "frameworks", "front_url",
		"froms", "header_length", "host", "hostname", "ip", "is_focus",
		"issuer", "matched", "matcher_name", "method", "midware", "mod",
		"name", "not_after", "not_before", "param", "password", "path",
		"port", "product", "protocol", "reason", "redirect_url", "san",
		"scheme", "serial", "service", "severity", "source", "source_url",
		"status", "status_code", "subject", "tags", "target", "technologies",
		"template_id", "template_name", "timestamp", "timing", "title", "uri",
		"url", "username", "value", "vendor", "version", "vuln_id", "vulns":
		return true
	default:
		return false
	}
}
