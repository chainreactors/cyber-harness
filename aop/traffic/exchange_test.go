package traffic

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func TestExchangeFromHTTPUsesCanonicalPairs(t *testing.T) {
	u, _ := url.Parse("https://example.test/a")
	req := &http.Request{Method: "POST", URL: u, Proto: "HTTP/1.1", Header: http.Header{"X-Test": {"a", "b"}}}
	resp := &http.Response{StatusCode: 201, Status: "201 Created", Header: http.Header{"Content-Type": {"application/json"}}}
	e := ExchangeFromHTTP(req, resp, []byte("req"), []byte("resp"))
	if e.Request.Method != "POST" || e.Request.URL != u.String() || !e.Complete {
		t.Fatalf("unexpected exchange: %+v", e)
	}
	if len(e.Request.Headers) != 2 || e.Response.StatusCode != 201 || string(e.Response.Body) != "resp" {
		t.Fatalf("unexpected canonical exchange: %+v", e)
	}
}

func TestFlowExchangeRoundTrip(t *testing.T) {
	flow := &Flow{
		Id:     "flow-1",
		ToolId: "call-9",
		Request: &HttpRequest{
			Method:   "POST",
			Url:      "https://example.test/login",
			Protocol: "HTTP/2.0",
			Headers: []*Header{
				{Name: "X-Trace", Value: "a"},
				{Name: "X-Trace", Value: "b"},
				{Name: "Content-Type", Value: "application/json"},
			},
			Body: []byte(`{"u":"n"}`),
		},
		Response: &HttpResponse{
			StatusCode:   302,
			ReasonPhrase: "Found",
			Headers:      []*Header{{Name: "Location", Value: "/home"}},
		},
		Complete: true,
	}

	exchange := ExchangeFromFlow(flow)
	if exchange.ID != "flow-1" || exchange.Response == nil || exchange.Response.StatusCode != 302 || !exchange.Complete {
		t.Fatalf("scalar fields did not cross: %#v", exchange)
	}
	if len(exchange.Request.Headers) != 3 || exchange.Request.Headers[1] != (Pair{Name: "X-Trace", Value: "b"}) {
		t.Fatalf("duplicate headers lost order or values: %#v", exchange.Request.Headers)
	}

	back := exchange.Proto()
	if back.GetToolId() != "" {
		t.Fatal("attribution must not cross into the exchange model")
	}
	if back.GetId() != flow.GetId() || back.GetResponse().GetReasonPhrase() != "Found" || len(back.GetRequest().GetHeaders()) != 3 {
		t.Fatalf("proto round-trip mismatch: %#v", back)
	}
}

func TestExchangeRequestOnly(t *testing.T) {
	flow := &Flow{
		Id:      "flow-2",
		Request: &HttpRequest{Method: "GET", Url: "http://unreachable.test/"},
		Error:   "dial tcp: connection refused",
	}
	exchange := ExchangeFromFlow(flow)
	if exchange.Response != nil {
		t.Fatalf("request-only flow gained a response: %#v", exchange.Response)
	}
	if exchange.Complete {
		t.Fatal("request-only flow must not be complete")
	}
	back := exchange.Proto()
	if back.GetResponse() != nil {
		t.Fatal("response must stay absent on the wire")
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
	const v1 = `{"id":"flow-1","request":{"method":"GET","url":"https://example.test/",` +
		`"headers":{"Accept":["text/html"],"X-Trace-Id":["a","b"]}},` +
		`"response":{"status_code":200,"body":"aGVsbG8="},"complete":true}`

	var exchange Exchange
	if err := json.Unmarshal([]byte(v1), &exchange); err != nil {
		t.Fatalf("decode v1 flow: %v", err)
	}
	if len(exchange.Request.Headers) != 3 {
		t.Fatalf("headers did not unfold to pairs: %#v", exchange.Request.Headers)
	}
	if exchange.Response == nil || exchange.Response.StatusCode != 200 {
		t.Fatalf("response did not cross: %#v", exchange.Response)
	}

	data, err := json.Marshal(exchange)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != v1 {
		t.Fatalf("persisted shape drifted:\n got %s\nwant %s", data, v1)
	}
}

// TestExchangeJSONRequestOnly pins the persisted form of an exchange that never
// got a response: no response key at all.
func TestExchangeJSONRequestOnly(t *testing.T) {
	data, err := json.Marshal(Exchange{
		ID:      "f",
		Request: Request{Method: "GET", URL: "http://x/"},
		Error:   "dial tcp: timeout",
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"f","request":{"method":"GET","url":"http://x/"},"error":"dial tcp: timeout","complete":false}`
	if string(data) != want {
		t.Fatalf(" got %s\nwant %s", data, want)
	}

	var exchange Exchange
	if err := json.Unmarshal([]byte(want), &exchange); err != nil {
		t.Fatal(err)
	}
	if exchange.Response != nil {
		t.Fatal("absent response key must stay nil")
	}
}

func TestExchangeJSONOmitsEmptyFields(t *testing.T) {
	data, err := json.Marshal(Exchange{
		ID:       "f",
		Request:  Request{Method: "GET", URL: "http://x/"},
		Response: &Response{StatusCode: 200},
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"f","request":{"method":"GET","url":"http://x/"},"response":{"status_code":200},"complete":false}`
	if string(data) != want {
		t.Fatalf(" got %s\nwant %s", data, want)
	}
}
