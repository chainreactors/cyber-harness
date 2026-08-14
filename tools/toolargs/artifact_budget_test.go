package toolargs

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	coretool "github.com/chainreactors/aiscan/core/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// katanaRecord is the JSON shape katana puts on the artifact channel:
// output.Result wrapping navigation.Request and navigation.Response. The URL is
// keyed `endpoint`, and bulk lands in four places — request.body carries a POST
// form the same way response.body carries a page, and each is shadowed by its
// raw transcript. A guard keyed on field names would have to know all four; a
// guard on string length reaches every one.
func katanaRecord(t *testing.T, bulk string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"timestamp": "2026-08-14T09:00:00Z",
		"request": map[string]any{
			"method":   "POST",
			"endpoint": "http://target/bundle.js",
			"body":     bulk,
			"raw":      bulk,
			"source":   "script",
		},
		"response": map[string]any{
			"status_code":    200,
			"content_length": len(bulk),
			"body":           bulk,
			"raw":            bulk,
			"technologies":   []any{"nginx"},
		},
	})
	require.NoError(t, err)
	return data
}

func TestArtifactWithinBudgetIsReturnedUntouched(t *testing.T) {
	data := katanaRecord(t, "ok")

	bounded := boundArtifactData(data)

	assert.Equal(t, string(data), string(bounded), "a record within budget must not be re-encoded")
}

// A katana crawl of a large page embeds the whole response body and the raw
// transcript of that same response. That single record can outgrow the frame
// that carries it; trimmed at birth, it stays a usable record instead of being
// dropped whole downstream.
func TestOversizedRecordKeepsItsFieldsAndLosesOnlyBulk(t *testing.T) {
	bulk := strings.Repeat("A", 5<<20)
	data := katanaRecord(t, bulk)
	require.Greater(t, len(data), maxArtifactDataBytes)

	bounded := boundArtifactData(data)

	require.LessOrEqual(t, len(bounded), maxArtifactDataBytes, "a trimmed record must fit the budget")
	require.Less(t, len(bounded), len(data))

	var record struct {
		Request struct {
			Method   string `json:"method"`
			Endpoint string `json:"endpoint"`
			Body     string `json:"body"`
			Raw      string `json:"raw"`
		} `json:"request"`
		Response struct {
			StatusCode    json.Number `json:"status_code"`
			ContentLength json.Number `json:"content_length"`
			Body          string      `json:"body"`
			Raw           string      `json:"raw"`
			Technologies  []string    `json:"technologies"`
		} `json:"response"`
	}
	require.NoError(t, json.Unmarshal(bounded, &record))
	// The small fields a control plane projects into its graph survive intact;
	// only the bulk that made the record unsendable is cut, and it says so.
	assert.Equal(t, "http://target/bundle.js", record.Request.Endpoint)
	assert.Equal(t, "POST", record.Request.Method)
	assert.Equal(t, "200", record.Response.StatusCode.String(), "numbers must survive a re-encode verbatim")
	assert.Equal(t, "5242880", record.Response.ContentLength.String())
	assert.Equal(t, []string{"nginx"}, record.Response.Technologies)
	for name, cut := range map[string]string{
		"request.body":  record.Request.Body,
		"request.raw":   record.Request.Raw,
		"response.body": record.Response.Body,
		"response.raw":  record.Response.Raw,
	} {
		assert.Contains(t, cut, "aiscan: truncated, 5242880 bytes total", name)
		assert.LessOrEqual(t, len(cut), maxArtifactStringBytes, name)
	}
}

// A per-string ceiling alone cannot bound a record whose bulk is spread across
// many modest values. The global encoder must stop the collection while keeping
// the projection fields that precede it.
func TestManySmallStringsAreBoundedAndNeverDropped(t *testing.T) {
	values := make([]any, 200000)
	for i := range values {
		values[i] = "row-of-modest-length-that-adds-up-across-many-entries"
	}
	data, err := json.Marshal(map[string]any{"endpoint": "http://target/list", "rows": values})
	require.NoError(t, err)
	require.Greater(t, len(data), maxArtifactDataBytes)

	bounded := boundArtifactData(data)

	require.NotNil(t, bounded)
	require.LessOrEqual(t, len(bounded), maxArtifactDataBytes, "aggregate short strings must still obey the hard budget")
	assert.LessOrEqual(t, len(bounded), len(data), "the result is never larger than the input")
	var probe struct {
		Endpoint string   `json:"endpoint"`
		Rows     []string `json:"rows"`
		Budget   struct {
			OriginalBytes int  `json:"original_bytes"`
			Truncated     bool `json:"truncated"`
		} `json:"_aiscan_artifact"`
	}
	require.NoError(t, json.Unmarshal(bounded, &probe), "the result is always valid JSON")
	assert.Equal(t, "http://target/list", probe.Endpoint)
	assert.NotEmpty(t, probe.Rows, "the record must retain useful collection data")
	assert.Less(t, len(probe.Rows), len(values), "the oversized collection must be bounded")
	assert.Equal(t, len(data), probe.Budget.OriginalBytes)
	assert.True(t, probe.Budget.Truncated)
}

func TestNearLimitStringsCannotGrowAnOversizedRecord(t *testing.T) {
	values := make([]any, 9000)
	for i := range values {
		values[i] = strings.Repeat("A", 513)
	}
	data, err := json.Marshal(map[string]any{"endpoint": "http://target/list", "rows": values})
	require.NoError(t, err)
	require.Greater(t, len(data), maxArtifactDataBytes)

	bounded := boundArtifactData(data)

	require.LessOrEqual(t, len(bounded), maxArtifactDataBytes)
	assert.Less(t, len(bounded), len(data), "a truncation marker must never make the record larger")
	assert.True(t, json.Valid(bounded))
}

func TestTruncationMarkerFitsInsideStringBudget(t *testing.T) {
	value := strings.Repeat("界", maxArtifactStringBytes)

	encoded, ok := marshalBoundedArtifactString(value, 512)

	require.True(t, ok)
	require.LessOrEqual(t, len(encoded), 512)
	var decoded string
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Contains(t, decoded, "aiscan: truncated")
	assert.True(t, utf8.ValidString(decoded))
	assert.Less(t, len(decoded), len(value))
}

func TestOversizedRecordKeepsNumberTextVerbatim(t *testing.T) {
	data, err := json.Marshal(map[string]any{
		"body":  strings.Repeat("A", 5<<20),
		"port":  json.Number("9007199254740993"),
		"ratio": json.Number("1e+09"),
	})
	require.NoError(t, err)

	bounded := boundArtifactData(data)

	decoder := json.NewDecoder(bytes.NewReader(bounded))
	decoder.UseNumber()
	var record map[string]any
	require.NoError(t, decoder.Decode(&record))
	assert.Equal(t, "9007199254740993", record["port"].(json.Number).String())
	assert.Equal(t, "1e+09", record["ratio"].(json.Number).String())
}

func TestInvalidOversizedJSONFallsBackToBoundedRecord(t *testing.T) {
	data := bytes.Repeat([]byte{'{'}, maxArtifactDataBytes+1)

	bounded := boundArtifactData(data)

	require.LessOrEqual(t, len(bounded), maxArtifactDataBytes)
	require.True(t, json.Valid(bounded))
	var record struct {
		Budget struct {
			OriginalBytes  int  `json:"original_bytes"`
			PayloadOmitted bool `json:"payload_omitted"`
			Truncated      bool `json:"truncated"`
		} `json:"_aiscan_artifact"`
	}
	require.NoError(t, json.Unmarshal(bounded, &record))
	assert.Equal(t, len(data), record.Budget.OriginalBytes)
	assert.True(t, record.Budget.PayloadOmitted)
	assert.True(t, record.Budget.Truncated)
}

func TestArtifactEmissionBoundsDataWithoutChangingIdentity(t *testing.T) {
	bus := eventbus.New[*aop.Event]()
	base := &Base{Events: bus}
	base.InitLogger(nil)
	data := map[string]any{
		"body":     strings.Repeat("A", 5<<20),
		"endpoint": "http://target/bundle.js",
	}
	wantID := ArtifactResultID("katana", toolpb.ArtifactKindWeb, "http://target/bundle.js", data)
	var artifact *toolpb.Artifact
	unsubscribe := bus.Subscribe(func(event *aop.Event) {
		decoded := new(toolpb.Artifact)
		if extension := event.GetExtension(); extension != nil && extension.UnmarshalTo(decoded) == nil {
			artifact = decoded
		}
	})
	defer unsubscribe()
	ctx := coretool.ContextWithInvocation(context.Background(), coretool.Invocation{CallID: "call-1"})

	base.EmitArtifactCtx(ctx, "katana", toolpb.ArtifactKindWeb, "http://target/bundle.js", data)

	require.NotNil(t, artifact)
	assert.Equal(t, wantID, artifact.GetResultId())
	assert.Equal(t, "call-1", artifact.GetCallId())
	require.LessOrEqual(t, len(artifact.GetData()), maxArtifactDataBytes)
	assert.True(t, json.Valid(artifact.GetData()))
}
