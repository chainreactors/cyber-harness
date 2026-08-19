package traffic

import (
	"encoding/json"
	"testing"
)

func TestFlowExchangeRoundTrip(t *testing.T) {
	flow := &Flow{
		Id:           "flow-1",
		ToolId:       "call-9",
		Method:       "POST",
		Url:          "https://example.test/login",
		Protocol:     "HTTP/2.0",
		StatusCode:   302,
		ReasonPhrase: "Found",
		RequestHeaders: []*Header{
			{Name: "X-Trace", Value: "a"},
			{Name: "X-Trace", Value: "b"},
			{Name: "Content-Type", Value: "application/json"},
		},
		ResponseHeaders: []*Header{{Name: "Location", Value: "/home"}},
		RequestBody:     []byte(`{"u":"n"}`),
		ResponseBody:    []byte(""),
		Error:           "",
		Complete:        true,
	}

	exchange := ExchangeFromFlow(flow)
	if exchange.ID != "flow-1" || exchange.StatusCode != 302 || !exchange.Complete {
		t.Fatalf("scalar fields did not cross: %#v", exchange)
	}
	if len(exchange.RequestHeaders) != 3 || exchange.RequestHeaders[1] != (Pair{Name: "X-Trace", Value: "b"}) {
		t.Fatalf("duplicate headers lost order or values: %#v", exchange.RequestHeaders)
	}

	back := exchange.Proto()
	if back.GetToolId() != "" {
		t.Fatal("attribution must not cross into the exchange model")
	}
	if back.GetId() != flow.GetId() || back.GetReasonPhrase() != "Found" || len(back.GetRequestHeaders()) != 3 {
		t.Fatalf("proto round-trip mismatch: %#v", back)
	}
}

func TestExchangeNilSafety(t *testing.T) {
	if ExchangeFromFlow(nil) != nil {
		t.Fatal("nil flow produced a non-nil exchange")
	}
	var exchange *Exchange
	if exchange.Proto() != nil {
		t.Fatal("nil exchange produced a non-nil flow")
	}
}

// TestExchangeJSONMatchesV1EvidenceShape pins the persisted form: the flow
// element of an http.exchange.v1 payload, headers as a name→values map.
func TestExchangeJSONMatchesV1EvidenceShape(t *testing.T) {
	const v1 = `{"id":"flow-1","method":"GET","url":"https://example.test/","status_code":200,` +
		`"request_headers":{"Accept":["text/html"],"X-Trace-Id":["a","b"]},` +
		`"response_body":"aGVsbG8=","complete":true}`

	var exchange Exchange
	if err := json.Unmarshal([]byte(v1), &exchange); err != nil {
		t.Fatalf("decode v1 flow: %v", err)
	}
	if len(exchange.RequestHeaders) != 3 {
		t.Fatalf("headers did not unfold to pairs: %#v", exchange.RequestHeaders)
	}

	data, err := json.Marshal(exchange)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != v1 {
		t.Fatalf("persisted shape drifted:\n got %s\nwant %s", data, v1)
	}
}

func TestExchangeJSONOmitsEmptyFields(t *testing.T) {
	data, err := json.Marshal(Exchange{ID: "f", Method: "GET", URL: "http://x/", StatusCode: 200})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"f","method":"GET","url":"http://x/","status_code":200,"complete":false}`
	if string(data) != want {
		t.Fatalf(" got %s\nwant %s", data, want)
	}
}
