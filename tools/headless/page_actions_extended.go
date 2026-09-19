//go:build full

package headless

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

var headlessKeyNames = map[string]input.Key{
	"enter": input.Enter, "tab": input.Tab, "escape": input.Escape,
	"backspace": input.Backspace, "delete": input.Delete, "space": input.Space,
	"arrowup": input.ArrowUp, "arrowdown": input.ArrowDown,
	"arrowleft": input.ArrowLeft, "arrowright": input.ArrowRight,
	"home": input.Home, "end": input.End,
	"pageup": input.PageUp, "pagedown": input.PageDown,
	"insert": input.Insert,
	"f1":     input.F1, "f2": input.F2, "f3": input.F3, "f4": input.F4,
	"f5": input.F5, "f6": input.F6, "f7": input.F7, "f8": input.F8,
	"f9": input.F9, "f10": input.F10, "f11": input.F11, "f12": input.F12,
	"shift": input.ShiftLeft, "control": input.ControlLeft, "ctrl": input.ControlLeft,
	"alt": input.AltLeft, "meta": input.MetaLeft, "command": input.MetaLeft,
}

func hasSelectorArgs(data map[string]string) bool {
	if data == nil {
		return false
	}
	return data["selector"] != "" || data["xpath"] != "" || data["js"] != "" ||
		data["query"] != "" || data["role"] != "" || data["label"] != "" ||
		data["text"] != "" || data["testid"] != ""
}

func resolveHeadlessKey(name string) (input.Key, error) {
	name = strings.TrimSpace(name)
	if key, ok := headlessKeyNames[strings.ToLower(name)]; ok {
		return key, nil
	}
	runes := []rune(name)
	if len(runes) == 1 {
		return input.Key(runes[0]), nil
	}
	return 0, fmt.Errorf("unknown key %q", name)
}

func pressKeys(page *rod.Page, expression string) error {
	parts := strings.Split(expression, "+")
	if len(parts) == 1 {
		key, err := resolveHeadlessKey(parts[0])
		if err != nil {
			return err
		}
		return page.Keyboard.Type(key)
	}

	actions := page.KeyActions()
	modifiers := make([]input.Key, 0, len(parts)-1)
	for _, part := range parts[:len(parts)-1] {
		key, err := resolveHeadlessKey(part)
		if err != nil {
			return fmt.Errorf("modifier: %w", err)
		}
		modifiers = append(modifiers, key)
		actions = actions.Press(key)
	}
	main, err := resolveHeadlessKey(parts[len(parts)-1])
	if err != nil {
		return err
	}
	actions = actions.Type(main)
	for i := len(modifiers) - 1; i >= 0; i-- {
		actions = actions.Release(modifiers[i])
	}
	return actions.Do()
}

func (p *Page) actionDblClick(act *Action, _ ActionData) error {
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("dblclick: %w", err)
	}
	return el.Click(proto.InputMouseButtonLeft, 2)
}

func (p *Page) actionHover(act *Action, _ ActionData) error {
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("hover: %w", err)
	}
	return el.Hover()
}

func (p *Page) actionFocus(act *Action, _ ActionData) error {
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("focus: %w", err)
	}
	return el.Focus()
}

func (p *Page) actionBlur(act *Action, _ ActionData) error {
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("blur: %w", err)
	}
	return el.Blur()
}

func (p *Page) actionCheck(act *Action, _ ActionData, checked bool) error {
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("checkbox: %w", err)
	}
	current, err := el.Property("checked")
	if err != nil {
		return fmt.Errorf("checkbox state: %w", err)
	}
	if current.Bool() == checked {
		return nil
	}
	if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("checkbox click: %w", err)
	}
	current, err = el.Property("checked")
	if err != nil {
		return fmt.Errorf("checkbox verify: %w", err)
	}
	if current.Bool() != checked {
		return fmt.Errorf("checkbox did not become checked=%t", checked)
	}
	return nil
}

func (p *Page) actionDispatchEvent(act *Action, _ ActionData) error {
	eventType := act.GetArg("event")
	if eventType == "" {
		eventType = act.GetArg("type")
	}
	if eventType == "" {
		return fmt.Errorf("dispatch: event argument required")
	}
	el, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("dispatch: %w", err)
	}
	detail := act.GetArg("detail")
	if detail == "" {
		detail = "null"
	} else if !json.Valid([]byte(detail)) {
		encoded, _ := json.Marshal(detail)
		detail = string(encoded)
	}
	_, err = el.Eval(`(eventType, detailJSON) => {
		const detail = JSON.parse(detailJSON);
		const options = {bubbles: true, cancelable: true};
		const event = detail === null ? new Event(eventType, options) : new CustomEvent(eventType, {...options, detail});
		this.dispatchEvent(event);
	}`, eventType, detail)
	return err
}

func (p *Page) actionSetViewport(act *Action, _ ActionData) error {
	width, err := positiveInt(act.GetArg("width"), "width")
	if err != nil {
		return fmt.Errorf("setviewport: %w", err)
	}
	height, err := positiveInt(act.GetArg("height"), "height")
	if err != nil {
		return fmt.Errorf("setviewport: %w", err)
	}
	scale := 1.0
	if value := act.GetArg("device-scale-factor"); value != "" {
		scale, err = strconv.ParseFloat(value, 64)
		if err != nil || scale <= 0 {
			return fmt.Errorf("setviewport: device-scale-factor must be positive")
		}
	}
	return p.page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width: width, Height: height, DeviceScaleFactor: scale,
	})
}

func positiveInt(value, name string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return n, nil
}

func (p *Page) actionWaitURL(act *Action, _ ActionData) error {
	expected := firstNonEmpty(act.GetArg("url"), act.GetArg("value"))
	if expected == "" {
		return fmt.Errorf("waiturl: url argument required")
	}
	return pollUntil(p.getTimeout(act), func() (bool, error) {
		info, err := p.page.Info()
		if err != nil {
			return false, err
		}
		return matchString(info.URL, expected, act.GetArg("match"))
	})
}

func (p *Page) actionWaitNetwork(act *Action, _ ActionData, response bool) error {
	expected := firstNonEmpty(act.GetArg("url"), act.GetArg("value"))
	if expected == "" {
		return fmt.Errorf("network wait: url argument required")
	}
	method := strings.ToUpper(act.GetArg("method"))
	return pollUntil(p.getTimeout(act), func() (bool, error) {
		p.mu.RLock()
		entries := append([]HistoryEntry(nil), p.History...)
		p.mu.RUnlock()
		for _, entry := range entries {
			if response && entry.StatusCode == 0 {
				continue
			}
			if method != "" && strings.ToUpper(entry.Method) != method {
				continue
			}
			matched, err := matchString(entry.URL, expected, act.GetArg("match"))
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	})
}

func pollUntil(timeout time.Duration, condition func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		ok, err := condition()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("condition not met within %s", timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func matchString(actual, expected, mode string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "contains":
		return strings.Contains(actual, expected), nil
	case "equals", "equal", "exact":
		return actual == expected, nil
	case "regex", "regexp":
		return regexp.MatchString(expected, actual)
	default:
		return false, fmt.Errorf("unknown match mode %q", mode)
	}
}

func normalizeStorageKind(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "local", "localstorage":
		return "localStorage", nil
	case "session", "sessionstorage":
		return "sessionStorage", nil
	default:
		return "", fmt.Errorf("storage type must be local or session")
	}
}

func (p *Page) actionStorage(act *Action, _ ActionData) error {
	kind, err := normalizeStorageKind(firstNonEmpty(act.GetArg("storage"), act.GetArg("type")))
	if err != nil {
		return err
	}
	operation := strings.ToLower(firstNonEmpty(act.GetArg("operation"), act.GetArg("op"), "set"))
	key := act.GetArg("key")
	switch operation {
	case "set":
		value, ok := act.Data["value"]
		if key == "" || !ok {
			return fmt.Errorf("storage set requires key and value")
		}
		_, err = p.page.Eval(`(kind, key, value) => window[kind].setItem(key, value)`, kind, key, value)
	case "delete", "remove":
		if key == "" {
			return fmt.Errorf("storage delete requires key")
		}
		_, err = p.page.Eval(`(kind, key) => window[kind].removeItem(key)`, kind, key)
	case "clear":
		_, err = p.page.Eval(`kind => window[kind].clear()`, kind)
	default:
		return fmt.Errorf("unknown storage operation %q", operation)
	}
	return err
}

func (p *Page) readStorage(kind, key string) (interface{}, error) {
	normalized, err := normalizeStorageKind(kind)
	if err != nil {
		return nil, err
	}
	if key != "" {
		result, evalErr := p.page.Eval(`(kind, key) => window[kind].getItem(key)`, normalized, key)
		if evalErr != nil {
			return nil, evalErr
		}
		if result.Value.Nil() {
			return "", nil
		}
		return result.Value.String(), nil
	}
	result, err := p.page.Eval(`kind => {
		const output = {};
		for (let i = 0; i < window[kind].length; i++) {
			const key = window[kind].key(i);
			output[key] = window[kind].getItem(key);
		}
		return output;
	}`, normalized)
	if err != nil {
		return nil, err
	}
	return result.Value.Val(), nil
}

func (p *Page) actionCookie(act *Action, _ ActionData) error {
	operation := strings.ToLower(firstNonEmpty(act.GetArg("operation"), act.GetArg("op"), "set"))
	name := act.GetArg("name")
	switch operation {
	case "set":
		value, ok := act.Data["value"]
		if name == "" || !ok {
			return fmt.Errorf("cookie set requires name and value")
		}
		cookieURL := act.GetArg("url")
		if cookieURL == "" {
			info, err := p.page.Info()
			if err != nil {
				return err
			}
			cookieURL = info.URL
		}
		cookie := &proto.NetworkCookieParam{
			Name: name, Value: value, URL: cookieURL,
			Domain: act.GetArg("domain"), Path: act.GetArg("path"),
			Secure:   strings.EqualFold(act.GetArg("secure"), "true"),
			HTTPOnly: strings.EqualFold(act.GetArg("http-only"), "true"),
		}
		return p.page.SetCookies([]*proto.NetworkCookieParam{cookie})
	case "delete", "remove":
		if name == "" {
			return fmt.Errorf("cookie delete requires name")
		}
		cookies, err := p.page.Cookies(nil)
		if err != nil {
			return err
		}
		for _, cookie := range cookies {
			if cookie.Name == name {
				if err := (proto.NetworkDeleteCookies{Name: cookie.Name, Domain: cookie.Domain, Path: cookie.Path}).Call(p.page); err != nil {
					return err
				}
			}
		}
		return nil
	case "clear":
		cookies, err := p.page.Cookies(nil)
		if err != nil {
			return err
		}
		for _, cookie := range cookies {
			if err := (proto.NetworkDeleteCookies{Name: cookie.Name, Domain: cookie.Domain, Path: cookie.Path}).Call(p.page); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown cookie operation %q", operation)
	}
}

func (p *Page) readCookie(name string) (interface{}, error) {
	cookies, err := p.page.Cookies(nil)
	if err != nil {
		return nil, err
	}
	if name == "" {
		values := make(map[string]string, len(cookies))
		for _, cookie := range cookies {
			values[cookie.Name] = cookie.Value
		}
		return values, nil
	}
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value, nil
		}
	}
	return "", nil
}

func (p *Page) actionAssert(act *Action, _ ActionData) error {
	kind := strings.ToLower(firstNonEmpty(act.GetArg("type"), act.GetArg("target")))
	expected := act.GetArg("value")
	var actual interface{}

	switch kind {
	case "url", "title":
		info, err := p.page.Info()
		if err != nil {
			return err
		}
		actual = info.URL
		if kind == "title" {
			actual = info.Title
		}
	case "storage":
		value, err := p.readStorage(act.GetArg("storage"), act.GetArg("key"))
		if err != nil {
			return err
		}
		actual = value
	case "cookie":
		value, err := p.readCookie(act.GetArg("name"))
		if err != nil {
			return err
		}
		actual = value
	default:
		if !hasSelectorArgs(act.Data) {
			return fmt.Errorf("assert %s requires a selector", kind)
		}
		el, err := p.pageElementBy(act.Data)
		if err != nil {
			if kind == "hidden" {
				return nil
			}
			return fmt.Errorf("resolve %s: %w", selectorSummary(act.Data), err)
		}
		switch kind {
		case "visible", "hidden":
			visible, err := el.Visible()
			if err != nil {
				return err
			}
			want := kind == "visible"
			if visible != want {
				return fmt.Errorf("expected element visible=%t", want)
			}
			return nil
		case "checked", "unchecked":
			checked, err := el.Property("checked")
			if err != nil {
				return err
			}
			want := kind == "checked"
			if checked.Bool() != want {
				return fmt.Errorf("expected element checked=%t", want)
			}
			return nil
		case "enabled", "disabled":
			disabled, err := el.Disabled()
			if err != nil {
				return err
			}
			wantDisabled := kind == "disabled"
			if disabled != wantDisabled {
				return fmt.Errorf("expected element disabled=%t", wantDisabled)
			}
			return nil
		case "text", "":
			actual, err = el.Text()
			if err != nil {
				return err
			}
		case "value":
			value, err := el.Property("value")
			if err != nil {
				return err
			}
			actual = value.String()
		case "attribute":
			attribute := act.GetArg("attribute")
			if attribute == "" {
				return fmt.Errorf("assert attribute requires attribute name")
			}
			value, err := el.Attribute(attribute)
			if err != nil {
				return fmt.Errorf("read attribute %q: %w", attribute, err)
			}
			if value != nil {
				actual = *value
			} else {
				actual = ""
			}
		default:
			return fmt.Errorf("unknown assertion type %q", kind)
		}
	}

	actualText := fmt.Sprint(actual)
	matched, err := matchString(actualText, expected, firstNonEmpty(act.GetArg("match"), "equals"))
	if err != nil {
		return err
	}
	if !matched {
		return fmt.Errorf("assertion failed: got %q, expected %s %q", actualText, firstNonEmpty(act.GetArg("match"), "equals"), expected)
	}
	return nil
}

func (p *Page) actionScroll(act *Action, _ ActionData) error {
	x, err := parseFloatDefault(act.GetArg("x"), 0)
	if err != nil {
		return fmt.Errorf("scroll x: %w", err)
	}
	y, err := parseFloatDefault(firstNonEmpty(act.GetArg("y"), act.GetArg("delta-y")), 0)
	if err != nil {
		return fmt.Errorf("scroll y: %w", err)
	}
	steps := 1
	if raw := act.GetArg("steps"); raw != "" {
		steps, err = positiveInt(raw, "steps")
		if err != nil {
			return err
		}
	}
	return p.page.Mouse.Scroll(x, y, steps)
}

func parseFloatDefault(value string, fallback float64) (float64, error) {
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseFloat(value, 64)
}

func (p *Page) actionDrag(act *Action, _ ActionData) error {
	source, err := p.pageElementBy(act.Data)
	if err != nil {
		return fmt.Errorf("drag source: %w", err)
	}
	targetSelector := act.GetArg("target")
	if targetSelector == "" {
		return fmt.Errorf("drag target selector required")
	}
	target, err := FindElement(p.page, targetSelector, p.getTimeout(act))
	if err != nil {
		return fmt.Errorf("drag target: %w", err)
	}
	if err := source.Hover(); err != nil {
		return err
	}
	if err := p.page.Mouse.Down(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}
	defer func() { _ = p.page.Mouse.Up(proto.InputMouseButtonLeft, 1) }()
	if err := target.Hover(); err != nil {
		return err
	}
	return p.page.Mouse.Up(proto.InputMouseButtonLeft, 1)
}

func (p *Page) actionReload(act *Action, _ ActionData) error {
	if err := p.page.Timeout(p.getTimeout(act)).Reload(); err != nil {
		return err
	}
	return p.page.Timeout(p.getTimeout(act)).WaitStable(defaultStableDur)
}

func (p *Page) actionHistoryNavigation(act *Action, _ ActionData, forward bool) error {
	page := p.page.Timeout(p.getTimeout(act))
	var err error
	if forward {
		err = page.NavigateForward()
	} else {
		err = page.NavigateBack()
	}
	if err != nil {
		return err
	}
	return page.WaitStable(defaultStableDur)
}

func (p *Page) actionSetContent(act *Action, _ ActionData) error {
	html, ok := act.Data["html"]
	if !ok {
		html, ok = act.Data["value"]
	}
	if !ok {
		return fmt.Errorf("setcontent: html argument required")
	}
	return p.page.SetDocumentContent(html)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func selectorSummary(data map[string]string) string {
	by := strings.ToLower(data["by"])
	if by == "" {
		return fmt.Sprintf("selector %q", data["selector"])
	}
	value := data[by]
	if by == "role" {
		value = data["role"] + " name=" + data["name"]
	}
	return fmt.Sprintf("%s selector %q", by, value)
}
