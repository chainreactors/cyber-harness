package traffic

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Pair is one HTTP header line: flat, ordered, duplicates preserved. It is the
// canonical header form both on the wire (proto Header) and in memory; a map
// cannot express order or repeated names.
type Pair struct {
	Name  string
	Value string
}

// Request is the request half of an exchange.
type Request struct {
	Method   string
	URL      string
	Protocol string
	Headers  []Pair
	Body     []byte
	BodyRef  *BodyRef `json:"-"`
}

// Response is the response half of an exchange. It is optional on Exchange: a
// request that never got a response (timeout, refused connection, one-way
// capture) has no response half.
type Response struct {
	StatusCode   int
	ReasonPhrase string
	Headers      []Pair
	Body         []byte
	BodyRef      *BodyRef `json:"-"`
}

// HydrateBodies loads file-backed request/response bodies into Body. It is
// intentionally explicit so list/query paths do not allocate large payloads.
func (e *Exchange) HydrateBodies() error {
	if e == nil {
		return nil
	}
	if e.Request.BodyRef != nil {
		body, err := ReadBody(e.Request.BodyRef)
		if err != nil {
			return err
		}
		e.Request.Body = body
		e.Request.BodyRef = nil
	}
	if e.Response != nil && e.Response.BodyRef != nil {
		body, err := ReadBody(e.Response.BodyRef)
		if err != nil {
			return err
		}
		e.Response.Body = body
		e.Response.BodyRef = nil
	}
	return nil
}

// Exchange is the canonical in-memory form of one captured HTTP exchange,
// composed of a request and an optional response. The Flow proto message is
// its wire view; the two are one model, converted by ExchangeFromFlow and
// Proto.
//
// Its JSON form is the flow element of the http.exchange.v1 evidence payload,
// where headers serialize as a name→values map for compatibility with the
// stored contract. Order and duplicate names survive in memory; the map view
// is the persisted projection.
type Exchange struct {
	ID       string
	Request  Request
	Response *Response
	Error    string
	Complete bool
}

// Clone returns an independent exchange value, including response metadata and
// body references. The proxy hot store uses it before hydrating a body so a
// query or subscriber never mutates the retained preview under a read lock.
func (e Exchange) Clone() Exchange {
	out := e
	out.Request.Headers = append([]Pair(nil), e.Request.Headers...)
	out.Request.Body = append([]byte(nil), e.Request.Body...)
	if e.Request.BodyRef != nil {
		ref := *e.Request.BodyRef
		out.Request.BodyRef = &ref
	}
	if e.Response != nil {
		resp := *e.Response
		resp.Headers = append([]Pair(nil), e.Response.Headers...)
		resp.Body = append([]byte(nil), e.Response.Body...)
		if e.Response.BodyRef != nil {
			ref := *e.Response.BodyRef
			resp.BodyRef = &ref
		}
		out.Response = &resp
	}
	return out
}

// ExchangeFromHTTP converts the standard library's request/response pair into
// the canonical HTTP observation model. Callers provide body bytes explicitly
// because the http bodies are streaming and may already have been consumed by
// the caller (for example, by a file-backed recorder).
func ExchangeFromHTTP(req *http.Request, resp *http.Response, requestBody, responseBody []byte) *Exchange {
	e := &Exchange{}
	if req != nil {
		urlString := ""
		if req.URL != nil {
			urlString = req.URL.String()
		}
		e.Request = Request{
			Method:   req.Method,
			URL:      urlString,
			Protocol: req.Proto,
			Headers:  PairsFromHTTP(req.Header),
			Body:     requestBody,
		}
	}
	if resp != nil {
		reason := resp.Status
		if prefix := strconv.Itoa(resp.StatusCode) + " "; strings.HasPrefix(reason, prefix) {
			reason = strings.TrimPrefix(reason, prefix)
		}
		e.Response = &Response{
			StatusCode:   resp.StatusCode,
			ReasonPhrase: reason,
			Headers:      PairsFromHTTP(resp.Header),
			Body:         responseBody,
		}
		e.Complete = true
	}
	return e
}

// WebSocketMessage is a single message observed after an HTTP WebSocket
// handshake. WebSocket traffic is deliberately modeled separately from an
// HTTP Exchange while sharing the same header pair representation.
type WebSocketMessage struct {
	Direction string
	Type      string
	Body      []byte
	Timestamp time.Time
}

// WebSocketExchange contains the handshake metadata and message stream for a
// WebSocket connection. The HTTP handshake itself can still be represented by
// Exchange; this type is for the bidirectional messages that follow it.
type WebSocketExchange struct {
	ID        string
	URL       string
	Protocol  string
	Headers   []Pair
	Messages  []WebSocketMessage
	StartTime time.Time
	EndTime   time.Time
	Complete  bool
	Error     string
}

// exchangeJSON is the persisted shape: identical field names and order to the
// http.exchange.v1 flow element, headers as a name→values map.
type exchangeJSON struct {
	ID       string        `json:"id"`
	Request  requestJSON   `json:"request"`
	Response *responseJSON `json:"response,omitempty"`
	Error    string        `json:"error,omitempty"`
	Complete bool          `json:"complete"`
}

type requestJSON struct {
	Method   string              `json:"method"`
	URL      string              `json:"url"`
	Protocol string              `json:"protocol,omitempty"`
	Headers  map[string][]string `json:"headers,omitempty"`
	Body     []byte              `json:"body,omitempty"`
}

type responseJSON struct {
	StatusCode   int                 `json:"status_code"`
	ReasonPhrase string              `json:"reason_phrase,omitempty"`
	Headers      map[string][]string `json:"headers,omitempty"`
	Body         []byte              `json:"body,omitempty"`
}

func (e Exchange) MarshalJSON() ([]byte, error) {
	wire := exchangeJSON{
		ID: e.ID,
		Request: requestJSON{
			Method:   e.Request.Method,
			URL:      e.Request.URL,
			Protocol: e.Request.Protocol,
			Headers:  pairsToMap(e.Request.Headers),
			Body:     e.Request.Body,
		},
		Error:    e.Error,
		Complete: e.Complete,
	}
	if e.Response != nil {
		wire.Response = &responseJSON{
			StatusCode:   e.Response.StatusCode,
			ReasonPhrase: e.Response.ReasonPhrase,
			Headers:      pairsToMap(e.Response.Headers),
			Body:         e.Response.Body,
		}
	}
	return json.Marshal(wire)
}

func (e *Exchange) UnmarshalJSON(data []byte) error {
	var wire exchangeJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*e = Exchange{
		ID: wire.ID,
		Request: Request{
			Method:   wire.Request.Method,
			URL:      wire.Request.URL,
			Protocol: wire.Request.Protocol,
			Headers:  mapToPairs(wire.Request.Headers),
			Body:     wire.Request.Body,
		},
		Error:    wire.Error,
		Complete: wire.Complete,
	}
	if wire.Response != nil {
		e.Response = &Response{
			StatusCode:   wire.Response.StatusCode,
			ReasonPhrase: wire.Response.ReasonPhrase,
			Headers:      mapToPairs(wire.Response.Headers),
			Body:         wire.Response.Body,
		}
	}
	return nil
}

// pairsToMap folds a pair sequence into the persisted map view, merging
// duplicate names in encounter order. Nil when empty so the key is omitted.
func pairsToMap(pairs []Pair) map[string][]string {
	if len(pairs) == 0 {
		return nil
	}
	out := make(map[string][]string, len(pairs))
	for _, p := range pairs {
		out[p.Name] = append(out[p.Name], p.Value)
	}
	return out
}

// mapToPairs unfolds the persisted map view. Keys are sorted so the in-memory
// form is deterministic even though the map lost the original order.
func mapToPairs(headers map[string][]string) []Pair {
	if len(headers) == 0 {
		return nil
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Pair, 0, len(headers))
	for _, name := range names {
		for _, value := range headers[name] {
			out = append(out, Pair{Name: name, Value: value})
		}
	}
	return out
}

// ExchangeFromFlow lifts a wire Flow into its canonical form. ToolId and
// Timestamp are attribution and transport metadata, not exchange semantics, so
// they do not cross over.
func ExchangeFromFlow(f *Flow) *Exchange {
	if f == nil {
		return nil
	}
	e := &Exchange{
		ID:       f.GetId(),
		Request:  requestFromProto(f.GetRequest()),
		Error:    f.GetError(),
		Complete: f.GetComplete(),
	}
	if r := f.GetResponse(); r != nil {
		resp := responseFromProto(r)
		e.Response = &resp
	}
	return e
}

// Proto renders the exchange as a wire Flow. Attribution (ToolId, Timestamp)
// is the caller's to stamp.
func (e *Exchange) Proto() *Flow {
	if e == nil {
		return nil
	}
	f := &Flow{
		Id:       e.ID,
		Request:  requestToProto(e.Request),
		Error:    e.Error,
		Complete: e.Complete,
	}
	if e.Response != nil {
		f.Response = responseToProto(*e.Response)
	}
	return f
}

func requestFromProto(r *HttpRequest) Request {
	if r == nil {
		return Request{}
	}
	return Request{
		Method:   r.GetMethod(),
		URL:      r.GetUrl(),
		Protocol: r.GetProtocol(),
		Headers:  pairsFromProto(r.GetHeaders()),
		Body:     r.GetBody(),
	}
}

func responseFromProto(r *HttpResponse) Response {
	return Response{
		StatusCode:   int(r.GetStatusCode()),
		ReasonPhrase: r.GetReasonPhrase(),
		Headers:      pairsFromProto(r.GetHeaders()),
		Body:         r.GetBody(),
	}
}

func requestToProto(r Request) *HttpRequest {
	return &HttpRequest{
		Method:   r.Method,
		Url:      r.URL,
		Protocol: r.Protocol,
		Headers:  pairsToProto(r.Headers),
		Body:     r.Body,
	}
}

func responseToProto(r Response) *HttpResponse {
	return &HttpResponse{
		StatusCode:   int32(r.StatusCode),
		ReasonPhrase: r.ReasonPhrase,
		Headers:      pairsToProto(r.Headers),
		Body:         r.Body,
	}
}

func pairsFromProto(headers []*Header) []Pair {
	if len(headers) == 0 {
		return nil
	}
	out := make([]Pair, 0, len(headers))
	for _, h := range headers {
		if h == nil {
			continue
		}
		out = append(out, Pair{Name: h.GetName(), Value: h.GetValue()})
	}
	return out
}

// PairsFromHTTP converts net/http headers into the canonical deterministic
// pair sequence used by Exchange and Flow.
func PairsFromHTTP(headers http.Header) []Pair {
	if len(headers) == 0 {
		return nil
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Pair, 0, len(headers))
	for _, name := range names {
		for _, value := range headers[name] {
			out = append(out, Pair{Name: name, Value: value})
		}
	}
	return out
}

func pairsToProto(pairs []Pair) []*Header {
	if len(pairs) == 0 {
		return nil
	}
	out := make([]*Header, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, &Header{Name: p.Name, Value: p.Value})
	}
	return out
}
