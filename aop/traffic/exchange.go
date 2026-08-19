package traffic

import (
	"encoding/json"
	"sort"
)

// Pair is one HTTP header line: flat, ordered, duplicates preserved. It is the
// canonical header form both on the wire (proto Header) and in memory; a map
// cannot express order or repeated names.
type Pair struct {
	Name  string
	Value string
}

// Exchange is the canonical in-memory form of one captured HTTP
// request/response pair. The Flow proto message is its wire view; the two are
// one model, converted by ExchangeFromFlow and Proto.
//
// Its JSON form is the flow element of the http.exchange.v1 evidence payload,
// where headers serialize as a name→values map for compatibility with the
// stored contract. Order and duplicate names survive in memory; the map view
// is the persisted projection.
type Exchange struct {
	ID              string
	Method          string
	URL             string
	Protocol        string
	StatusCode      int
	ReasonPhrase    string
	RequestHeaders  []Pair
	ResponseHeaders []Pair
	RequestBody     []byte
	ResponseBody    []byte
	Error           string
	Complete        bool
}

// exchangeJSON is the persisted shape: identical field names and order to the
// http.exchange.v1 flow element, headers as a name→values map.
type exchangeJSON struct {
	ID              string              `json:"id"`
	Method          string              `json:"method"`
	URL             string              `json:"url"`
	Protocol        string              `json:"protocol,omitempty"`
	StatusCode      int                 `json:"status_code"`
	ReasonPhrase    string              `json:"reason_phrase,omitempty"`
	RequestHeaders  map[string][]string `json:"request_headers,omitempty"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	RequestBody     []byte              `json:"request_body,omitempty"`
	ResponseBody    []byte              `json:"response_body,omitempty"`
	Error           string              `json:"error,omitempty"`
	Complete        bool                `json:"complete"`
}

func (e Exchange) MarshalJSON() ([]byte, error) {
	return json.Marshal(exchangeJSON{
		ID:              e.ID,
		Method:          e.Method,
		URL:             e.URL,
		Protocol:        e.Protocol,
		StatusCode:      e.StatusCode,
		ReasonPhrase:    e.ReasonPhrase,
		RequestHeaders:  pairsToMap(e.RequestHeaders),
		ResponseHeaders: pairsToMap(e.ResponseHeaders),
		RequestBody:     e.RequestBody,
		ResponseBody:    e.ResponseBody,
		Error:           e.Error,
		Complete:        e.Complete,
	})
}

func (e *Exchange) UnmarshalJSON(data []byte) error {
	var wire exchangeJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*e = Exchange{
		ID:              wire.ID,
		Method:          wire.Method,
		URL:             wire.URL,
		Protocol:        wire.Protocol,
		StatusCode:      wire.StatusCode,
		ReasonPhrase:    wire.ReasonPhrase,
		RequestHeaders:  mapToPairs(wire.RequestHeaders),
		ResponseHeaders: mapToPairs(wire.ResponseHeaders),
		RequestBody:     wire.RequestBody,
		ResponseBody:    wire.ResponseBody,
		Error:           wire.Error,
		Complete:        wire.Complete,
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
	return &Exchange{
		ID:              f.GetId(),
		Method:          f.GetMethod(),
		URL:             f.GetUrl(),
		Protocol:        f.GetProtocol(),
		StatusCode:      int(f.GetStatusCode()),
		ReasonPhrase:    f.GetReasonPhrase(),
		RequestHeaders:  pairsFromProto(f.GetRequestHeaders()),
		ResponseHeaders: pairsFromProto(f.GetResponseHeaders()),
		RequestBody:     f.GetRequestBody(),
		ResponseBody:    f.GetResponseBody(),
		Error:           f.GetError(),
		Complete:        f.GetComplete(),
	}
}

// Proto renders the exchange as a wire Flow. Attribution (ToolId, Timestamp)
// is the caller's to stamp.
func (e *Exchange) Proto() *Flow {
	if e == nil {
		return nil
	}
	return &Flow{
		Id:              e.ID,
		Method:          e.Method,
		Url:             e.URL,
		Protocol:        e.Protocol,
		StatusCode:      int32(e.StatusCode),
		ReasonPhrase:    e.ReasonPhrase,
		RequestHeaders:  pairsToProto(e.RequestHeaders),
		ResponseHeaders: pairsToProto(e.ResponseHeaders),
		RequestBody:     e.RequestBody,
		ResponseBody:    e.ResponseBody,
		Error:           e.Error,
		Complete:        e.Complete,
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
