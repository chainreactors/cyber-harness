package toolargs

import (
	"encoding/json"
	"strings"
	"testing"

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
	}
}

// data reaching this function is always json.Marshal output, so it always
// parses and truncatedStrings always reaches its strings. The contract holds
// even for a record whose bulk is spread across many small strings rather than
// a few large ones: never nil, never larger than the input.
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
	assert.LessOrEqual(t, len(bounded), len(data), "the result is never larger than the input")
	var probe map[string]any
	require.NoError(t, json.Unmarshal(bounded, &probe), "the result is always valid JSON")
	assert.Equal(t, "http://target/list", probe["endpoint"])
}
