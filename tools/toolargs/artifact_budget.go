package toolargs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// A control plane delivers one artifact event as one frame and bounds a frame:
// past its limit the frame is dropped, and past a hard limit the transport
// closes the connection outright — taking every other tool call sharing it down
// with it. A scanner-native record is the one artifact with no natural bound: a
// single katana crawl result carries the response body next to the raw
// transcript of that same response, and katana's own body ceiling alone is 4
// MiB, so one record can reach eight to ten.
//
// This is the sole place an artifact is built (every tool reaches the wire
// through EmitArtifactResultCtx), and the record is structured JSON here, before
// it becomes opaque bytes downstream. So it is bounded here: at birth, where the
// bulk can be cut from the body and transcript while the small fields a control
// plane projects — URL, host, port, status, technology — are kept intact, and
// where no record is ever lost, only trimmed. A downstream frame guard, working
// from bytes it cannot parse, could only drop the whole record.
const (
	maxArtifactDataBytes   = 4 << 20
	maxArtifactStringBytes = 64 << 10
)

// boundArtifactData returns data unchanged when it is within budget, and
// otherwise a copy of the record with its oversized strings truncated. data is
// the output of json.Marshal, so it always parses; the guard on the decode error
// is defensive. The returned bytes are never nil and never longer than the input
// — a pathological record that stays over budget after the tightest pass is sent
// as-is-but-smaller for the frame guard to bound, never dropped here.
func boundArtifactData(data []byte) []byte {
	if len(data) <= maxArtifactDataBytes {
		return data
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var record any
	if err := decoder.Decode(&record); err != nil {
		return data
	}
	var last []byte
	for _, limit := range []int{maxArtifactStringBytes, 8 << 10, 512} {
		shrunk, err := json.Marshal(truncatedStrings(record, limit))
		if err != nil {
			return data
		}
		last = shrunk
		if len(shrunk) <= maxArtifactDataBytes {
			return shrunk
		}
	}
	return last
}

// truncatedStrings copies value with every string longer than limit truncated.
// It copies instead of mutating so a tighter pass starts from the untouched
// record rather than re-truncating its own markers, keeps numbers as their
// original text (UseNumber) so a re-encode cannot round a port or an id, and
// drops the partial rune a byte cut leaves behind — an artifact that is not
// valid UTF-8 fails to marshal further down the pipeline.
func truncatedStrings(value any, limit int) any {
	switch typed := value.(type) {
	case string:
		if len(typed) <= limit {
			return typed
		}
		return strings.ToValidUTF8(typed[:limit], "") + fmt.Sprintf("…[aiscan: truncated, %d bytes total]", len(typed))
	case []any:
		items := make([]any, len(typed))
		for i, item := range typed {
			items[i] = truncatedStrings(item, limit)
		}
		return items
	case map[string]any:
		fields := make(map[string]any, len(typed))
		for name, item := range typed {
			fields[name] = truncatedStrings(item, limit)
		}
		return fields
	}
	return value
}
