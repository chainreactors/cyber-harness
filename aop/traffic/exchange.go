package traffic

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// FlowFromHTTP converts the standard library's request/response pair into the
// canonical traffic protobuf. Callers provide body bytes explicitly because
// HTTP bodies are streaming and may already have been consumed.
func FlowFromHTTP(req *http.Request, resp *http.Response, requestBody, responseBody []byte) *Flow {
	flow := &Flow{}
	if req != nil {
		urlString := ""
		if req.URL != nil {
			urlString = req.URL.String()
		}
		flow.Request = &HttpRequest{
			Method:   req.Method,
			Url:      urlString,
			Protocol: req.Proto,
			Headers:  HeadersFromHTTPWithHost(req.Header, req.Host),
			Body:     requestBody,
		}
	}
	if resp != nil {
		reason := resp.Status
		if prefix := strconv.Itoa(resp.StatusCode) + " "; strings.HasPrefix(reason, prefix) {
			reason = strings.TrimPrefix(reason, prefix)
		}
		flow.Response = &HttpResponse{
			StatusCode:   int32(resp.StatusCode),
			ReasonPhrase: reason,
			Headers:      HeadersFromHTTP(resp.Header),
			Body:         responseBody,
		}
		flow.Complete = true
	}
	return flow
}

// HeadersFromHTTP converts net/http headers into a deterministic sequence that
// preserves repeated names.
func HeadersFromHTTP(headers http.Header) []*Header {
	if len(headers) == 0 {
		return nil
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*Header, 0, len(headers))
	for _, name := range names {
		for _, value := range headers[name] {
			out = append(out, &Header{Name: name, Value: value})
		}
	}
	return out
}

func containsHeaderName(headers []*Header, name string) bool {
	for _, header := range headers {
		if header != nil && strings.EqualFold(header.GetName(), name) {
			return true
		}
	}
	return false
}

// HeadersFromHTTPWithHost adds the Host header that net/http stores separately
// from Request.Header. An existing Host header is never duplicated.
func HeadersFromHTTPWithHost(headers http.Header, host string) []*Header {
	values := HeadersFromHTTP(headers)
	if host == "" || containsHeaderName(values, "Host") {
		return values
	}
	return append([]*Header{{Name: "Host", Value: host}}, values...)
}
