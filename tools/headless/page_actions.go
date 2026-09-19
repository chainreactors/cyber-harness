//go:build full

// Individual action handlers, ported from nuclei engine/page_actions.go.
// All action args are resolved via act.GetArg() with the action's already-interpolated Data.

package headless

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// actionNavigate performs navigation to a URL.
// Ported from nuclei: merges action URL params with input URL params.
func (p *Page) actionNavigate(act *Action, out ActionData) error {
	rawURL := act.GetArg("url")
	if rawURL == "" {
		return fmt.Errorf("navigate: url argument required")
	}

	// Merge params from inputURL if set (nuclei param auto-merge behavior).
	if p.inputURL != nil {
		parsedNav, err := url.Parse(rawURL)
		if err == nil && parsedNav.Host != "" {
			inputParams := p.inputURL.Query()
			navParams := parsedNav.Query()
			for k, vs := range inputParams {
				if _, exists := navParams[k]; !exists {
					for _, v := range vs {
						navParams.Add(k, v)
					}
				}
			}
			parsedNav.RawQuery = navParams.Encode()
			rawURL = parsedNav.String()
		}
	}

	if err := p.page.Navigate(rawURL); err != nil {
		return fmt.Errorf("navigate to %s: %w", rawURL, err)
	}
	_ = p.page.WaitLoad()

	info, _ := p.page.Info()
	if info != nil {
		out["url"] = info.URL
	}

	// Update request log if instance is available.
	if p.instance != nil && act.GetArg("url") != "" {
		p.instance.requestLog[act.GetArg("url")] = rawURL
	}

	return nil
}

func (p *Page) actionScript(act *Action, out ActionData) error {
	code := act.GetArg("code")
	if code == "" {
		return fmt.Errorf("script: code argument required")
	}

	if act.GetArg("hook") == "true" {
		if _, err := p.page.EvalOnNewDocument(code); err != nil {
			return err
		}
	}

	data, err := p.page.Eval(code)
	if err != nil {
		return err
	}
	if data != nil && act.Name != "" {
		out[act.Name] = data.Value.String()
	}
	return nil
}

func (p *Page) actionClick(act *Action, out ActionData) error {
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("click: %w", err)
	}
	if err := el.ScrollIntoView(); err != nil {
		return fmt.Errorf("click scroll: %w", err)
	}
	return el.Click(proto.InputMouseButtonLeft, 1)
}

func (p *Page) actionRightClick(act *Action, out ActionData) error {
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("rightclick: %w", err)
	}
	if err := el.ScrollIntoView(); err != nil {
		return fmt.Errorf("rightclick scroll: %w", err)
	}
	return el.Click(proto.InputMouseButtonRight, 1)
}

func (p *Page) actionTextInput(act *Action, out ActionData) error {
	value, ok := act.Data["value"]
	if !ok {
		return fmt.Errorf("text: value argument required")
	}
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("text: %w", err)
	}
	if err := el.ScrollIntoView(); err != nil {
		return fmt.Errorf("text scroll: %w", err)
	}
	if act.GetArg("clear") == "true" {
		if err := el.SelectAllText(); err != nil {
			return fmt.Errorf("text clear: %w", err)
		}
	}
	return el.Input(value)
}

func (p *Page) actionScreenshot(act *Action, out ActionData) error {
	to := act.GetArg("to")
	if to == "" {
		to = "screenshot"
	}

	var data []byte
	var err error
	if hasSelectorArgs(act.Data) {
		var el *rod.Element
		el, err = p.pageElementBy(act.Data)
		if err == nil {
			data, err = el.Screenshot(proto.PageCaptureScreenshotFormatPng, 90)
		}
	} else {
		fullpage := act.GetArg("fullpage") == "true"
		data, err = p.page.Screenshot(fullpage, &proto.PageCaptureScreenshot{})
	}
	if err != nil {
		return fmt.Errorf("screenshot: %w", err)
	}

	if !strings.HasSuffix(to, ".png") {
		to += ".png"
	}
	to, _ = filepath.Abs(to)

	if act.GetArg("mkdir") == "true" {
		if dir := filepath.Dir(to); dir != "." {
			_ = os.MkdirAll(dir, 0700)
		}
	}

	if err := os.WriteFile(to, data, 0640); err != nil {
		return fmt.Errorf("screenshot write: %w", err)
	}
	if act.Name != "" {
		out[act.Name] = to
	}
	return nil
}

func (p *Page) actionTimeInput(act *Action, out ActionData) error {
	value := act.GetArg("value")
	if value == "" {
		return fmt.Errorf("time: value argument required")
	}
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("time: %w", err)
	}
	if err := el.ScrollIntoView(); err != nil {
		return fmt.Errorf("time scroll: %w", err)
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return fmt.Errorf("time parse %q: %w", value, err)
	}
	return el.InputTime(t)
}

func (p *Page) actionSelectInput(act *Action, out ActionData) error {
	value := act.GetArg("value")
	if value == "" {
		return fmt.Errorf("select: value argument required")
	}
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	if err := el.ScrollIntoView(); err != nil {
		return fmt.Errorf("select scroll: %w", err)
	}
	selected := !strings.EqualFold(act.GetArg("selected"), "false")
	selectorType := selectorBy(act.GetArg("selector"))
	if err := el.Select([]string{value}, selected, selectorType); err == nil {
		return nil
	}
	return el.Select([]string{fmt.Sprintf("option[value=%s]", strconv.Quote(value))}, selected, rod.SelectorTypeCSSSector)
}

func (p *Page) actionFilesInput(act *Action, out ActionData) error {
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("files: %w", err)
	}
	if err := el.ScrollIntoView(); err != nil {
		return fmt.Errorf("files scroll: %w", err)
	}
	value := act.GetArg("value")
	paths := strings.Split(value, ",")
	for i := range paths {
		paths[i] = strings.TrimSpace(paths[i])
	}
	return el.SetFiles(paths)
}

func (p *Page) actionWaitLifecycle(act *Action, out ActionData, event proto.PageLifecycleEventName) error {
	timeout := p.getTimeout(act)
	wait := p.page.Timeout(timeout).WaitNavigation(event)
	wait()
	return nil
}

func (p *Page) actionWaitStable(act *Action, out ActionData) error {
	dur := defaultStableDur
	if d := act.GetArg("duration"); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil {
			dur = parsed
		}
	}
	timeout := p.getTimeout(act)
	return p.page.Timeout(timeout).WaitStable(dur)
}

func (p *Page) actionWaitIdle(act *Action, out ActionData) error {
	idle := 500 * time.Millisecond
	if value := act.GetArg("duration"); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			idle = parsed
		}
	}
	wait := p.page.Timeout(p.getTimeout(act)).WaitRequestIdle(idle, nil, nil, nil)
	wait()
	return nil
}

func (p *Page) actionWaitVisible(act *Action, out ActionData) error {
	if !hasSelectorArgs(act.Data) {
		return fmt.Errorf("waitvisible: selector argument required")
	}
	timeout := p.getTimeout(act)
	el, err := ElementBy(p.page, act.Data, timeout)
	if err != nil {
		return fmt.Errorf("waitvisible: %w", err)
	}
	return el.WaitVisible()
}

func (p *Page) actionGetResource(act *Action, out ActionData) error {
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("getresource: %w", err)
	}
	resource, err := el.Resource()
	if err != nil {
		return fmt.Errorf("getresource: %w", err)
	}
	if act.Name != "" {
		out[act.Name] = string(resource)
	}
	return nil
}

func (p *Page) actionExtract(act *Action, out ActionData) error {
	target := act.GetArg("target")
	if target == "url" || target == "title" {
		info, err := p.page.Info()
		if err != nil {
			return fmt.Errorf("extract %s: %w", target, err)
		}
		value := info.URL
		if target == "title" {
			value = info.Title
		}
		if act.Name != "" {
			out[act.Name] = value
		}
		return nil
	}
	if target == "storage" {
		value, err := p.readStorage(act.GetArg("storage"), act.GetArg("key"))
		if err != nil {
			return err
		}
		if act.Name != "" {
			out[act.Name] = value
		}
		return nil
	}
	if target == "cookie" {
		value, err := p.readCookie(act.GetArg("name"))
		if err != nil {
			return err
		}
		if act.Name != "" {
			out[act.Name] = value
		}
		return nil
	}

	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	if err := el.ScrollIntoView(); err != nil {
		return fmt.Errorf("extract scroll: %w", err)
	}

	switch target {
	case "attribute":
		attr := act.GetArg("attribute")
		if attr == "" {
			return fmt.Errorf("extract: attribute name required")
		}
		val, err := el.Attribute(attr)
		if err != nil {
			return err
		}
		if act.Name != "" {
			if val != nil {
				out[act.Name] = *val
			} else {
				out[act.Name] = ""
			}
		}
	case "html":
		html, err := el.HTML()
		if err != nil {
			return err
		}
		if act.Name != "" {
			out[act.Name] = html
		}
	case "value":
		value, err := el.Property("value")
		if err != nil {
			return err
		}
		if act.Name != "" {
			out[act.Name] = value.String()
		}
	case "property":
		property := act.GetArg("property")
		if property == "" {
			return fmt.Errorf("extract: property name required")
		}
		value, err := el.Property(property)
		if err != nil {
			return err
		}
		if act.Name != "" {
			out[act.Name] = value.Val()
		}
	default:
		text, err := el.Text()
		if err != nil {
			return err
		}
		if act.Name != "" {
			out[act.Name] = text
		}
	}
	return nil
}

func (p *Page) actionKeyboard(act *Action, out ActionData) error {
	keys := act.GetArg("keys")
	if keys == "" {
		return fmt.Errorf("keyboard: keys argument required")
	}
	if hasSelectorArgs(act.Data) {
		el, err := p.pageElementBy(act.Data)
		if err != nil {
			return fmt.Errorf("keyboard selector: %w", err)
		}
		if err := el.Focus(); err != nil {
			return fmt.Errorf("keyboard focus: %w", err)
		}
	}
	return pressKeys(p.page, keys)
}

func (p *Page) actionSleep(act *Action, out ActionData) error {
	dur := 5 * time.Second
	if d := act.GetArg("duration"); d != "" {
		if n, err := strconv.Atoi(d); err == nil {
			dur = time.Duration(n) * time.Second
		} else if parsed, err := time.ParseDuration(d); err == nil {
			dur = parsed
		}
	}
	time.Sleep(dur)
	return nil
}

// actionWaitEvent waits for an arbitrary CDP event.
// Ported from nuclei: uses proto.GetType for event reflection.
func (p *Page) actionWaitEvent(act *Action, out ActionData) (func() error, error) {
	event := act.GetArg("event")
	if event == "" {
		return nil, fmt.Errorf("waitevent: event argument required")
	}

	maxDuration := 5 * time.Second
	if d := act.GetArg("max-duration"); d != "" {
		if n, err := strconv.Atoi(d); err == nil {
			maxDuration = time.Duration(n) * time.Second
		} else if parsed, err := time.ParseDuration(d); err == nil {
			maxDuration = parsed
		}
	}

	// Use proto.GetType for arbitrary CDP event reflection.
	gotType := proto.GetType(event)
	if gotType == nil {
		// Fallback to WaitStable for unknown events.
		return func() error {
			return p.page.Timeout(maxDuration).WaitStable(defaultStableDur)
		}, nil
	}

	tmp, ok := reflect.New(gotType).Interface().(proto.Event)
	if !ok {
		return nil, fmt.Errorf("event %q is not a valid page event", event)
	}

	waitEvent := tmp
	waitFunc := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), maxDuration)
		defer cancel()

		done := make(chan struct{})
		go func() {
			p.page.WaitEvent(waitEvent)()
			close(done)
		}()

		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return nil
		}
	}

	return waitFunc, nil
}

func (p *Page) actionDialog(act *Action, out ActionData) error {
	wait, handle := p.page.MustHandleDialog()
	accept := !strings.EqualFold(act.GetArg("accept"), "false")
	prompt := act.GetArg("prompt")
	go func() {
		wait()
		handle(accept, prompt)
	}()
	return nil
}

// actionWaitDialog waits for a JS dialog and captures type + message.
// Ported from nuclei engine/page_actions.go:HandleDialog.
func (p *Page) actionWaitDialog(act *Action, out ActionData) error {
	maxDuration := 10 * time.Second
	if d := act.GetArg("max-duration"); d != "" {
		if n, err := strconv.Atoi(d); err == nil {
			maxDuration = time.Duration(n) * time.Second
		} else if parsed, err := time.ParseDuration(d); err == nil {
			maxDuration = parsed
		}
	}

	type dialogResult struct {
		dialog *proto.PageJavascriptDialogOpening
		err    error
	}
	ch := make(chan dialogResult, 1)

	wait, handle := p.page.HandleDialog()
	accept := !strings.EqualFold(act.GetArg("accept"), "false")
	prompt := act.GetArg("prompt")
	go func() {
		dialog := wait()
		err := handle(&proto.PageHandleJavaScriptDialog{
			Accept:     accept,
			PromptText: prompt,
		})
		ch <- dialogResult{dialog: dialog, err: err}
	}()

	select {
	case res := <-ch:
		if res.err == nil && act.Name != "" {
			out[act.Name] = true
			out[act.Name+"_type"] = string(res.dialog.Type)
			out[act.Name+"_message"] = res.dialog.Message
		}
		return res.err
	case <-time.After(maxDuration):
		if act.Name != "" {
			out[act.Name] = false
		}
		return nil
	}
}

func (p *Page) getTimeout(act *Action) time.Duration {
	if t := act.GetArg("timeout"); t != "" {
		if n, err := strconv.Atoi(t); err == nil {
			return time.Duration(n) * time.Second
		}
		if d, err := time.ParseDuration(t); err == nil {
			return d
		}
	}
	return defaultActionTimeout
}

func selectorBy(s string) rod.SelectorType {
	switch s {
	case "r", "regex":
		return rod.SelectorTypeRegex
	case "css":
		return rod.SelectorTypeCSSSector
	default:
		return rod.SelectorTypeText
	}
}
