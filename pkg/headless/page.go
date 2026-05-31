//go:build browser

// Page wraps a go-rod page and executes headless action sequences.
// Ported from nuclei pkg/protocols/headless/engine/page.go.

package headless

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const (
	defaultActionTimeout = 10 * time.Second
	defaultStableDur     = time.Second
)

// HistoryEntry captures a request/response pair.
type HistoryEntry struct {
	RawRequest  string
	RawResponse string
}

// Page wraps a go-rod page and executes headless action sequences.
type Page struct {
	page     *rod.Page
	instance *Instance
	engine   *Engine
	mu       sync.RWMutex

	// inputURL is the parsed target URL for parameter merging in NavigateURL.
	inputURL *url.URL

	// variables holds the merged context: template vars + payload + action outputs.
	variables map[string]interface{}

	// Network interception (nuclei dual-path: HijackRouter for modifications, native Hijack for capture-only).
	rules        []rule
	hijackRouter *rod.HijackRouter
	hijackNative *Hijack

	// history stores all request/response pairs captured during hijack.
	History []HistoryEntry

	// statusCode and responseHeaders from the first navigation.
	statusCode      int
	responseHeaders map[string]string

	// lastActionNavigate tracks the last navigate action for request logging.
	lastActionNavigate *Action
}

// NewPage creates a Page executor around a go-rod page.
func NewPage(page *rod.Page, engine *Engine, variables map[string]interface{}) *Page {
	if variables == nil {
		variables = make(map[string]interface{})
	}
	return &Page{
		page:            page,
		engine:          engine,
		variables:       variables,
		responseHeaders: make(map[string]string),
	}
}

// NewPageWithInstance creates a Page with instance isolation support.
func NewPageWithInstance(page *rod.Page, instance *Instance, variables map[string]interface{}) *Page {
	p := NewPage(page, instance.engine, variables)
	p.instance = instance
	return p
}

// SetInputURL sets the target URL for parameter merging in navigate actions.
func (p *Page) SetInputURL(u *url.URL) {
	p.inputURL = u
}

// Page returns the underlying rod page.
func (p *Page) RodPage() *rod.Page {
	return p.page
}

// hasModificationRules checks if any pending rules exist.
func (p *Page) hasModificationRules() bool {
	return len(p.rules) > 0
}

// hasResponseRules checks if any rules target the response part.
func (p *Page) hasResponseRules() bool {
	for _, r := range p.rules {
		if r.Part == "response" {
			return true
		}
	}
	return false
}

// ExecuteActions runs a sequence of actions on the page.
// Returns accumulated output data from named actions.
func (p *Page) ExecuteActions(actions []*Action) (ActionData, error) {
	// Set up interception before any navigation.
	// Dual-path: modification rules → HijackRouter, else → native Fetch hijack.
	if actionsContainModifications(actions) {
		p.collectRules(actions)
	}
	p.setupHijack()

	out := make(ActionData)
	var waitFuncs []func() error

	for _, act := range actions {
		resolved := act.Interpolate(p.variables)

		var err error
		switch resolved.ActionType.ActionType {
		case ActionNavigate:
			err = p.actionNavigate(resolved, out)
			if err == nil {
				for _, wf := range waitFuncs {
					if wf != nil {
						if werr := wf(); werr != nil {
							return out, fmt.Errorf("wait after navigate: %w", werr)
						}
					}
				}
				waitFuncs = nil
				p.lastActionNavigate = resolved
			}
		case ActionScript:
			err = p.actionScript(resolved, out)
		case ActionClick:
			err = p.actionClick(resolved, out)
		case ActionRightClick:
			err = p.actionRightClick(resolved, out)
		case ActionTextInput:
			err = p.actionTextInput(resolved, out)
		case ActionScreenshot:
			err = p.actionScreenshot(resolved, out)
		case ActionTimeInput:
			err = p.actionTimeInput(resolved, out)
		case ActionSelectInput:
			err = p.actionSelectInput(resolved, out)
		case ActionFilesInput:
			err = p.actionFilesInput(resolved, out)
		case ActionWaitDOM:
			err = p.actionWaitLifecycle(resolved, out, proto.PageLifecycleEventNameDOMContentLoaded)
		case ActionWaitFCP:
			err = p.actionWaitLifecycle(resolved, out, proto.PageLifecycleEventNameFirstContentfulPaint)
		case ActionWaitFMP:
			err = p.actionWaitLifecycle(resolved, out, proto.PageLifecycleEventNameFirstMeaningfulPaint)
		case ActionWaitIdle:
			err = p.actionWaitLifecycle(resolved, out, proto.PageLifecycleEventNameNetworkIdle)
		case ActionWaitLoad:
			err = p.actionWaitLifecycle(resolved, out, proto.PageLifecycleEventNameLoad)
		case ActionWaitStable:
			err = p.actionWaitStable(resolved, out)
		case ActionWaitVisible:
			err = p.actionWaitVisible(resolved, out)
		case ActionGetResource:
			err = p.actionGetResource(resolved, out)
		case ActionExtract:
			err = p.actionExtract(resolved, out)
		case ActionSetMethod, ActionAddHeader, ActionSetHeader, ActionDeleteHeader, ActionSetBody:
			// Rules already pre-collected in collectRules. No-op during execution.
		case ActionWaitEvent:
			var wf func() error
			wf, err = p.actionWaitEvent(resolved, out)
			if wf != nil {
				waitFuncs = append(waitFuncs, wf)
			}
		case ActionKeyboard:
			err = p.actionKeyboard(resolved, out)
		case ActionDebug:
			// no-op
		case ActionSleep:
			err = p.actionSleep(resolved, out)
		case ActionDialog:
			err = p.actionDialog(resolved, out)
		case ActionWaitDialog:
			err = p.actionWaitDialog(resolved, out)
		default:
			continue
		}

		if err != nil {
			return out, fmt.Errorf("action %s: %w", resolved.ActionType.String(), err)
		}

		// Store named output into variables for subsequent interpolation.
		if resolved.Name != "" {
			for k, v := range out {
				p.variables[k] = v
			}
		}
	}
	return out, nil
}

// collectRules pre-scans actions for modification rules before navigation.
func (p *Page) collectRules(actions []*Action) {
	for _, act := range actions {
		if !containsModificationActionType(act.ActionType.ActionType) {
			continue
		}
		resolved := act.Interpolate(p.variables)
		part := resolved.GetArg("part")
		if part == "" {
			part = "request"
		}
		args := make(map[string]string)
		for k, v := range resolved.Data {
			args[k] = v
		}
		p.rules = append(p.rules, newRule(resolved.ActionType.ActionType, part, args))
	}
}

// setupHijack configures interception using the nuclei dual-path pattern:
//   - Modification rules exist → rod.HijackRequests + routingRuleHandler (full HTTP client)
//   - No modification rules → native Fetch hijack + routingRuleHandlerNative (capture only)
//
// Both paths capture request/response history.
func (p *Page) setupHijack() {
	if p.hasModificationRules() {
		p.setupHijackRouter()
	} else {
		p.setupNativeHijack()
	}
}

// setupHijackRouter starts rod's HijackRequests with full request/response modification.
// Ported from nuclei page.go:120-125.
func (p *Page) setupHijackRouter() {
	if p.hijackRouter != nil {
		return
	}
	httpClient := p.engine.getHTTPClient()
	router := p.page.HijackRequests()
	router.MustAdd("*", p.routingRuleHandler(httpClient))
	go router.Run()
	p.hijackRouter = router
}

// setupNativeHijack starts native CDP Fetch interception for capture-only.
// Ported from nuclei page.go:127-138.
func (p *Page) setupNativeHijack() {
	if p.hijackNative != nil {
		return
	}
	hijack := NewHijack(p.page)
	hijack.SetPattern(&proto.FetchRequestPattern{
		URLPattern:   "*",
		RequestStage: proto.FetchRequestStageResponse,
	})
	go func() {
		_ = hijack.Start(p.routingRuleHandlerNative)()
	}()
	p.hijackNative = hijack
}

// routingRuleHandler handles request/response modification via HijackRouter.
// Ported from nuclei engine/rules.go:16-109.
func (p *Page) routingRuleHandler(httpClient *http.Client) func(ctx *rod.Hijack) {
	return func(ctx *rod.Hijack) {
		ctx.Request.Req().ContentLength = int64(len(ctx.Request.Body()))

		p.mu.RLock()
		rules := p.rules
		p.mu.RUnlock()

		// Apply request-part rules.
		for i := range rules {
			r := &rules[i]
			if r.Part != "request" {
				continue
			}
			switch r.Action {
			case ActionSetMethod:
				r.Do(func() {
					ctx.Request.Req().Method = r.Args["method"]
				})
			case ActionAddHeader:
				ctx.Request.Req().Header.Add(r.Args["key"], r.Args["value"])
			case ActionSetHeader:
				ctx.Request.Req().Header.Set(r.Args["key"], r.Args["value"])
			case ActionDeleteHeader:
				ctx.Request.Req().Header.Del(r.Args["key"])
			case ActionSetBody:
				body := r.Args["body"]
				ctx.Request.Req().ContentLength = int64(len(body))
				ctx.Request.SetBody(body)
			}
		}

		// Load response via HTTP client.
		if err := ctx.LoadResponse(httpClient, true); err != nil {
			ctx.ContinueRequest(&proto.FetchContinueRequest{})
			return
		}

		// Apply response-part rules.
		for i := range rules {
			r := &rules[i]
			if r.Part != "response" {
				continue
			}
			switch r.Action {
			case ActionAddHeader:
				ctx.Response.Headers().Add(r.Args["key"], r.Args["value"])
			case ActionSetHeader:
				ctx.Response.Headers().Set(r.Args["key"], r.Args["value"])
			case ActionDeleteHeader:
				ctx.Response.Headers().Del(r.Args["key"])
			case ActionSetBody:
				body := r.Args["body"]
				ctx.Response.Headers().Set("Content-Length", fmt.Sprintf("%d", len(body)))
				ctx.Response.SetBody(body)
			}
		}

		// Capture history.
		p.captureHijackHistory(ctx)
	}
}

// captureHijackHistory records request/response from a HijackRouter callback.
func (p *Page) captureHijackHistory(ctx *rod.Hijack) {
	req := ctx.Request.Req()
	var rawReq string
	if raw, err := httputil.DumpRequestOut(req, true); err == nil {
		rawReq = string(raw)
	}

	var rawResp strings.Builder
	payload := ctx.Response.Payload()
	if payload != nil {
		fmt.Fprintf(&rawResp, "HTTP/1.1 %d %s\r\n", payload.ResponseCode, payload.ResponsePhrase)
		for _, h := range payload.ResponseHeaders {
			rawResp.WriteString(h.Name + ": " + h.Value + "\r\n")
		}
		rawResp.WriteString("\r\n")
		rawResp.WriteString(ctx.Response.Body())
	}

	p.addHistory(rawReq, rawResp.String(), payload)
}

// routingRuleHandlerNative handles capture-only interception via native CDP Fetch.
// Ported from nuclei engine/rules.go:112-157.
func (p *Page) routingRuleHandlerNative(e *proto.FetchRequestPaused) error {
	body, _ := FetchGetResponseBody(p.page, e)

	var statusCode int
	if e.ResponseStatusCode != nil {
		statusCode = *e.ResponseStatusCode
	}

	// Reconstruct raw request.
	var rawReq strings.Builder
	fmt.Fprintf(&rawReq, "%s %s HTTP/1.1\r\n", e.Request.Method, e.Request.URL)
	for _, header := range e.Request.Headers {
		fmt.Fprintf(&rawReq, "%s\r\n", header.String())
	}
	if e.Request.HasPostData {
		fmt.Fprintf(&rawReq, "\r\n%s", e.Request.PostData)
	}

	// Reconstruct raw response.
	var rawResp strings.Builder
	fmt.Fprintf(&rawResp, "HTTP/1.1 %d %s\r\n", statusCode, e.ResponseStatusText)
	for _, header := range e.ResponseHeaders {
		rawResp.WriteString(header.Name + ": " + header.Value + "\r\n")
	}
	rawResp.WriteString("\r\n")
	rawResp.Write(body)

	// Store history and first-navigation status/headers.
	p.mu.Lock()
	if p.statusCode == 0 && statusCode > 0 {
		p.statusCode = statusCode
		for _, h := range e.ResponseHeaders {
			p.responseHeaders[h.Name] = h.Value
		}
	}
	p.History = append(p.History, HistoryEntry{
		RawRequest:  rawReq.String(),
		RawResponse: rawResp.String(),
	})
	p.mu.Unlock()

	return FetchContinueRequest(p.page, e)
}

// addHistory records a request/response pair from the HijackRouter path.
func (p *Page) addHistory(rawReq, rawResp string, payload *proto.FetchFulfillRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.statusCode == 0 && payload != nil {
		p.statusCode = payload.ResponseCode
		for _, h := range payload.ResponseHeaders {
			p.responseHeaders[h.Name] = h.Value
		}
	}
	p.History = append(p.History, HistoryEntry{
		RawRequest:  rawReq,
		RawResponse: rawResp,
	})
}

// Close cleans up any resources held by the page.
func (p *Page) Close() {
	if p.hijackRouter != nil {
		_ = p.hijackRouter.Stop()
		p.hijackRouter = nil
	}
	if p.hijackNative != nil {
		_ = p.hijackNative.Stop()
		p.hijackNative = nil
	}
}

// pageElementBy resolves a page element using nuclei's selector conventions.
func (p *Page) pageElementBy(data map[string]string) (*rod.Element, error) {
	by := data["by"]
	page := p.page.Timeout(defaultActionTimeout)
	switch by {
	case "x", "xpath":
		xpath := data["xpath"]
		if xpath == "" {
			return nil, fmt.Errorf("xpath selector required")
		}
		return page.ElementX(xpath)
	case "js":
		return page.ElementByJS(&rod.EvalOptions{JS: data["js"]})
	case "r", "regex":
		return page.ElementR(data["selector"], data["regex"])
	case "search":
		elms, err := page.Search(data["query"])
		if err != nil {
			return nil, err
		}
		if elms.First != nil {
			return elms.First, nil
		}
		return nil, fmt.Errorf("no element found for query: %s", data["query"])
	default:
		sel := data["selector"]
		if sel == "" {
			return nil, fmt.Errorf("no selector provided")
		}
		return page.Element(sel)
	}
}

// ResponseData captures HTTP response info from the navigated page.
type ResponseData struct {
	URL        string
	StatusCode int
	Headers    map[string]string
	Body       string
	History    []HistoryEntry
}

// GetResponseData extracts the page's current state for matchers/extractors.
func (p *Page) GetResponseData() *ResponseData {
	rd := &ResponseData{
		Headers: make(map[string]string),
	}
	info, err := p.page.Info()
	if err == nil && info != nil {
		rd.URL = info.URL
	}
	html, err := p.page.HTML()
	if err == nil {
		rd.Body = html
	}

	p.mu.RLock()
	rd.StatusCode = p.statusCode
	for k, v := range p.responseHeaders {
		rd.Headers[k] = v
	}
	rd.History = make([]HistoryEntry, len(p.History))
	copy(rd.History, p.History)
	p.mu.RUnlock()

	return rd
}
