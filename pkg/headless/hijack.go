//go:build browser

// Ported from nuclei pkg/protocols/headless/engine/hijack.go.
// Native CDP Fetch-domain based hijack for response capture without modification.

package headless

import (
	"encoding/base64"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// Hijack is a native CDP Fetch-domain based hijack handler.
// Used when no request/response modification rules exist (the common path).
// Captures all request/response pairs for the history DSL variable.
type Hijack struct {
	page    *rod.Page
	enable  *proto.FetchEnable
	disable *proto.FetchDisable
	cancel  func()
}

// HijackHandler is the callback type for intercepted requests.
type HijackHandler = func(e *proto.FetchRequestPaused) error

// NewHijack creates a native Fetch-domain hijack from a page.
func NewHijack(page *rod.Page) *Hijack {
	return &Hijack{
		page:    page,
		disable: &proto.FetchDisable{},
	}
}

// SetPattern configures the URL pattern and request stage to intercept.
func (h *Hijack) SetPattern(pattern *proto.FetchRequestPattern) {
	h.enable = &proto.FetchEnable{
		Patterns: []*proto.FetchRequestPattern{pattern},
	}
}

// Start begins interception and returns a wait function.
func (h *Hijack) Start(handler HijackHandler) func() error {
	if h.enable == nil {
		panic("hijack pattern not set")
	}

	p, cancel := h.page.WithCancel()
	h.cancel = cancel

	if err := h.enable.Call(p); err != nil {
		return func() error { return err }
	}

	wait := p.EachEvent(func(e *proto.FetchRequestPaused) {
		if handler != nil {
			_ = handler(e)
		}
	})

	return func() error {
		wait()
		return nil
	}
}

// Stop disables the Fetch interception.
func (h *Hijack) Stop() error {
	if h.cancel != nil {
		h.cancel()
	}
	return h.disable.Call(h.page)
}

// FetchGetResponseBody retrieves the response body for an intercepted request.
func FetchGetResponseBody(page *rod.Page, e *proto.FetchRequestPaused) ([]byte, error) {
	m := proto.FetchGetResponseBody{RequestID: e.RequestID}
	r, err := m.Call(page)
	if err != nil {
		return nil, err
	}
	if !r.Base64Encoded {
		return []byte(r.Body), nil
	}
	return base64.StdEncoding.DecodeString(r.Body)
}

// FetchContinueRequest continues a paused request without modification.
func FetchContinueRequest(page *rod.Page, e *proto.FetchRequestPaused) error {
	m := proto.FetchContinueRequest{RequestID: e.RequestID}
	return m.Call(page)
}
