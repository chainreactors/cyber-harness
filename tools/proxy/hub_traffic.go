package proxy

import (
	"net/http"
	"strconv"
	"sync"

	traffic "github.com/chainreactors/aiscan/aop/traffic"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h *ProxyHub) Store() *FlowStore { return h.store }

func (h *ProxyHub) ingest(flow Flow) {
	if !h.recording.Load() {
		return
	}
	stored := h.store.Add(flow)
	h.publish(&stored)
}

func (h *ProxyHub) publish(flow *Flow) {
	if flow == nil {
		return
	}
	h.subsMu.Lock()
	if len(h.subs) == 0 {
		h.subsMu.Unlock()
		return
	}
	message := flowToProto(flow)
	for _, subscriber := range h.subs {
		select {
		case subscriber <- message:
		default:
		}
	}
	h.subsMu.Unlock()
}

func (h *ProxyHub) Subscribe(buffer int) (<-chan *traffic.Flow, func()) {
	if buffer <= 0 {
		buffer = 256
	}
	channel := make(chan *traffic.Flow, buffer)
	h.subsMu.Lock()
	if h.subs == nil {
		h.subs = make(map[int]chan *traffic.Flow)
	}
	id := h.nextSub
	h.nextSub++
	h.subs[id] = channel
	h.subsMu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.subsMu.Lock()
			if existing, ok := h.subs[id]; ok {
				delete(h.subs, id)
				close(existing)
			}
			h.subsMu.Unlock()
		})
	}
	return channel, cancel
}

func flowToProto(flow *Flow) *traffic.Flow {
	if flow == nil {
		return nil
	}
	message := &traffic.Flow{
		Id:              strconv.Itoa(flow.ID),
		ToolId:          flow.ToolID,
		Method:          flow.Method,
		Url:             flow.URL,
		StatusCode:      int32(flow.StatusCode),
		RequestHeaders:  headersToProto(flow.RequestHeaders),
		ResponseHeaders: headersToProto(flow.ResponseHeaders),
		RequestBody:     flow.RequestBodySnip,
		ResponseBody:    flow.ResponseBodySnip,
		Error:           flow.Error,
		Complete:        flow.Error == "" && flow.StatusCode != 0,
	}
	if !flow.Timestamp.IsZero() {
		message.Timestamp = timestamppb.New(flow.Timestamp)
	}
	return message
}

func headersToProto(headers http.Header) []*traffic.Header {
	var result []*traffic.Header
	for name, values := range headers {
		for _, value := range values {
			result = append(result, &traffic.Header{Name: name, Value: value})
		}
	}
	return result
}
