package traffic

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func TestFlowFromHTTPUsesCanonicalHeaders(t *testing.T) {
	u, _ := url.Parse("https://example.test/a")
	req := &http.Request{Method: "POST", URL: u, Proto: "HTTP/1.1", Header: http.Header{"X-Test": {"a", "b"}}}
	resp := &http.Response{StatusCode: 201, Status: "201 Created", Header: http.Header{"Content-Type": {"application/json"}}}
	flow := FlowFromHTTP(req, resp, []byte("req"), []byte("resp"))
	if flow.GetRequest().GetMethod() != "POST" || flow.GetRequest().GetUrl() != u.String() || !flow.GetComplete() {
		t.Fatalf("unexpected flow: %+v", flow)
	}
	if len(flow.GetRequest().GetHeaders()) != 2 || flow.GetResponse().GetStatusCode() != 201 || string(flow.GetResponse().GetBody()) != "resp" {
		t.Fatalf("unexpected canonical flow: %+v", flow)
	}
}

func TestHeadersFromHTTPWithHost(t *testing.T) {
	got := HeadersFromHTTPWithHost(http.Header{"Accept": {"*/*"}}, "example.test:8090")
	if len(got) != 2 || got[0].GetName() != "Host" || got[0].GetValue() != "example.test:8090" {
		t.Fatalf("Host not prepended: %#v", got)
	}
	if got := HeadersFromHTTPWithHost(http.Header{"Accept": {"*/*"}}, ""); len(got) != 1 {
		t.Fatalf("empty host should not add a header: %#v", got)
	}
	got = HeadersFromHTTPWithHost(http.Header{"host": {"already.test"}}, "example.test")
	if len(got) != 1 || !containsHeaderName(got, "Host") {
		t.Fatalf("existing Host must not be duplicated: %#v", got)
	}
}

func TestFlowFromHTTPAddsHost(t *testing.T) {
	u, _ := url.Parse("https://example.test:8443/a")
	req := &http.Request{Method: "GET", URL: u, Host: "example.test:8443", Proto: "HTTP/1.1", Header: http.Header{"Accept": {"*/*"}}}
	flow := FlowFromHTTP(req, nil, nil, nil)
	headers := flow.GetRequest().GetHeaders()
	if len(headers) == 0 || headers[0].GetName() != "Host" || headers[0].GetValue() != "example.test:8443" {
		t.Fatalf("Host header not synthesized from req.Host: %#v", headers)
	}
}

func TestFlowFromHTTPRequestOnly(t *testing.T) {
	flow := FlowFromHTTP(&http.Request{Method: "GET"}, nil, nil, nil)
	if flow.GetResponse() != nil || flow.GetComplete() {
		t.Fatalf("request-only flow gained a response: %#v", flow)
	}
}

func TestFlowJSONPersistenceUsesProtoShape(t *testing.T) {
	want := &Flow{
		Id: "flow-1",
		Request: &HttpRequest{
			Method: "GET",
			Url:    "https://example.test/",
			Headers: []*Header{
				{Name: "Accept", Value: "text/html"},
				{Name: "X-Trace-Id", Value: "a"},
				{Name: "X-Trace-Id", Value: "b"},
			},
		},
		Response: &HttpResponse{StatusCode: 200, Body: []byte("hello")},
		Complete: true,
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Flow
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.GetId() != want.GetId() || got.GetResponse().GetStatusCode() != 200 || len(got.GetRequest().GetHeaders()) != 3 {
		t.Fatalf("persisted flow mismatch: %#v", &got)
	}
}
