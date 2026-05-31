//go:build browser

package playwright

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
)

// ---------------------------------------------------------------------------
// click
// ---------------------------------------------------------------------------

func (c *Command) execClick(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright click: usage: playwright click <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright click: element %q not found: %w", selector, err)
		}
		if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return "", fmt.Errorf("playwright click: %w", err)
		}
		_ = page.WaitStable(waitStableDur)

		tagRes, _ := el.Eval(`() => this.tagName`)
		tag := ""
		if tagRes != nil {
			tag = tagRes.Value.Str()
		}
		info, _ := page.Info()
		url := ""
		if info != nil {
			url = info.URL
		}
		return fmt.Sprintf("Clicked <%s> %q\nCurrent URL: %s", strings.ToLower(tag), selector, url), nil
	})
}

// ---------------------------------------------------------------------------
// fill
// ---------------------------------------------------------------------------

func (c *Command) execFill(ctx context.Context, args []string) (string, error) {
	if len(args) < 3 {
		return "", fmt.Errorf("playwright fill: usage: playwright fill <session> <selector> <value>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := args[1]
	value := strings.Join(args[2:], " ")

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright fill: element %q not found: %w", selector, err)
		}
		_ = el.SelectAllText()
		if err := el.Input(value); err != nil {
			return "", fmt.Errorf("playwright fill: %w", err)
		}
		return fmt.Sprintf("Filled %q with %q", selector, value), nil
	})
}

// ---------------------------------------------------------------------------
// select
// ---------------------------------------------------------------------------

func (c *Command) execSelect(ctx context.Context, args []string) (string, error) {
	if len(args) < 3 {
		return "", fmt.Errorf("playwright select: usage: playwright select-option <session> <selector> <value>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := args[1]
	value := strings.Join(args[2:], " ")

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright select: element %q not found: %w", selector, err)
		}
		if err := selectOption(el, value); err != nil {
			return "", fmt.Errorf("playwright select: %w", err)
		}
		return fmt.Sprintf("Selected %q in %q", value, selector), nil
	})
}

// ---------------------------------------------------------------------------
// wait
// ---------------------------------------------------------------------------

func (c *Command) execWait(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright wait: usage: playwright wait-for <session> <selector|--idle|--stable>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	target := strings.Join(args[1:], " ")

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		switch target {
		case "--idle":
			wait := page.WaitRequestIdle(500*time.Millisecond, nil, nil, nil)
			wait()
			return "Network idle", nil
		case "--stable":
			_ = page.WaitStable(500 * time.Millisecond)
			return "DOM stable", nil
		default:
			el, err := findElement(page, target)
			if err != nil {
				return "", fmt.Errorf("playwright wait: element %q did not appear: %w", target, err)
			}
			_ = el.WaitVisible()
			return fmt.Sprintf("Element %q visible", target), nil
		}
	})
}

// ---------------------------------------------------------------------------
// session text extraction
// ---------------------------------------------------------------------------

func (c *Command) execSessionText(ctx context.Context, args []string, commandName string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright %s: session name required", commandName)
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	selector := "body"
	if len(args) > 1 {
		selector = strings.Join(args[1:], " ")
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright %s: element %q not found: %w", commandName, selector, err)
		}
		text, err := el.Text()
		if err != nil {
			return "", fmt.Errorf("playwright %s: %w", commandName, err)
		}
		return formatTextOutput(selector, text), nil
	})
}

// ---------------------------------------------------------------------------
// session HTML extraction
// ---------------------------------------------------------------------------

func (c *Command) execSessionContent(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright content: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		if len(args) > 1 {
			selector := strings.Join(args[1:], " ")
			el, err := findElement(page, selector)
			if err != nil {
				return "", fmt.Errorf("playwright content: element %q not found: %w", selector, err)
			}
			html, err := el.HTML()
			if err != nil {
				return "", fmt.Errorf("playwright content: %w", err)
			}
			return formatHTMLOutput(selector, html), nil
		}

		html, err := page.HTML()
		if err != nil {
			return "", fmt.Errorf("playwright content: %w", err)
		}
		info, _ := page.Info()
		url := ""
		if info != nil {
			url = info.URL
		}
		return formatHTMLOutput(url, html), nil
	})
}

// ---------------------------------------------------------------------------
// session-aware JS eval
// ---------------------------------------------------------------------------

func (c *Command) execSessionEval(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright eval: usage: playwright evaluate <url|session> <script>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	script := strings.Join(args[1:], " ")

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		jsFunc := fmt.Sprintf("() => (%s)", script)
		res, err := page.Eval(jsFunc)
		if err != nil {
			return "", fmt.Errorf("playwright eval: %w", err)
		}

		var result string
		if res.Value.Nil() {
			result = "undefined"
		} else {
			raw, _ := json.MarshalIndent(res.Value, "", "  ")
			result = string(raw)
		}
		return fmt.Sprintf("Script: %s\n---\n%s", script, result), nil
	})
}

// ---------------------------------------------------------------------------
// session-aware screenshot with optional --selector
// ---------------------------------------------------------------------------

func (c *Command) execSessionScreenshot(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright screenshot: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	output := ""
	selector := ""
	fullPage := false

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--output":
			if i+1 < len(args) {
				i++
				output = args[i]
			} else {
				return "", fmt.Errorf("playwright screenshot: --output requires a value")
			}
		case "--selector":
			if i+1 < len(args) {
				i++
				selector = args[i]
			} else {
				return "", fmt.Errorf("playwright screenshot: --selector requires a value")
			}
		case "--full-page":
			fullPage = true
		default:
			return "", fmt.Errorf("playwright screenshot: unknown flag: %s", args[i])
		}
	}

	if output == "" {
		output = fmt.Sprintf("screenshot_%d.png", time.Now().Unix())
	}
	outPath := resolvePath(c.workDir, output)

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		var data []byte
		if selector != "" {
			el, elErr := findElement(page, selector)
			if elErr != nil {
				return "", fmt.Errorf("playwright screenshot: element %q not found: %w", selector, elErr)
			}
			data, err = el.Screenshot(proto.PageCaptureScreenshotFormatPng, 90)
		} else if fullPage {
			data, err = page.Screenshot(true, &proto.PageCaptureScreenshot{
				Format:  proto.PageCaptureScreenshotFormatPng,
				Quality: gson.Int(90),
			})
		} else {
			data, err = page.Screenshot(false, &proto.PageCaptureScreenshot{
				Format:  proto.PageCaptureScreenshotFormatPng,
				Quality: gson.Int(90),
			})
		}
		if err != nil {
			return "", fmt.Errorf("playwright screenshot: capture: %w", err)
		}

		if err := writeFile(outPath, data); err != nil {
			return "", fmt.Errorf("playwright screenshot: write: %w", err)
		}

		abs, _ := filepath.Abs(outPath)
		return fmt.Sprintf("Screenshot saved: %s\nSize: %d bytes\nSelector: %s\nFull-page: %v",
			abs, len(data), selector, fullPage), nil
	})
}

// ---------------------------------------------------------------------------
// url: current page URL and title
// ---------------------------------------------------------------------------

func (c *Command) execURL(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright url: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		info, err := page.Info()
		if err != nil {
			return "", fmt.Errorf("playwright url: %w", err)
		}
		return fmt.Sprintf("URL: %s\nTitle: %s", info.URL, info.Title), nil
	})
}

// ---------------------------------------------------------------------------
// cookies: list / set / clear
// ---------------------------------------------------------------------------

func (c *Command) execCookies(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright cookies: usage: playwright cookies <session> --list|--set k=v|--clear")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	flag := args[1]

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		switch flag {
		case "--list":
			cookies, err := page.Cookies(nil)
			if err != nil {
				return "", fmt.Errorf("playwright cookies: %w", err)
			}
			if len(cookies) == 0 {
				return "No cookies", nil
			}
			data, _ := json.MarshalIndent(cookies, "", "  ")
			return fmt.Sprintf("Cookies (%d):\n%s", len(cookies), string(data)), nil

		case "--set":
			if len(args) < 3 {
				return "", fmt.Errorf("playwright cookies --set: requires name=value")
			}
			info, _ := page.Info()
			domain := ""
			if info != nil {
				domain = info.URL
			}
			var cookies []*proto.NetworkCookieParam
			for _, pair := range args[2:] {
				kv := strings.SplitN(pair, "=", 2)
				if len(kv) != 2 {
					continue
				}
				cookies = append(cookies, &proto.NetworkCookieParam{
					Name:  kv[0],
					Value: kv[1],
					URL:   domain,
				})
			}
			if len(cookies) == 0 {
				return "", fmt.Errorf("playwright cookies --set: no valid name=value pairs")
			}
			if err := page.SetCookies(cookies); err != nil {
				return "", fmt.Errorf("playwright cookies --set: %w", err)
			}
			return fmt.Sprintf("Set %d cookie(s)", len(cookies)), nil

		case "--clear":
			cookies, _ := page.Cookies(nil)
			for _, ck := range cookies {
				_ = proto.NetworkDeleteCookies{
					Name:   ck.Name,
					Domain: ck.Domain,
				}.Call(page)
			}
			return fmt.Sprintf("Cleared %d cookie(s)", len(cookies)), nil

		default:
			return "", fmt.Errorf("playwright cookies: unknown flag %q (expected --list, --set, or --clear)", flag)
		}
	})
}

// ---------------------------------------------------------------------------
// session network capture (start/dump/stop)
// ---------------------------------------------------------------------------

func (c *Command) execSessionNetwork(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright network: usage: playwright network <url|session> --start|--dump|--stop")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	flag := args[1]

	switch flag {
	case "--start":
		return networkCaptureStart(ctx, sess)
	case "--dump":
		return networkCaptureDump(sess)
	case "--stop":
		return networkCaptureStop(ctx, sess)
	default:
		return "", fmt.Errorf("playwright network: unknown flag %q", flag)
	}
}

func networkCaptureStart(ctx context.Context, sess *Session) (string, error) {
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		sess.networkMu.Lock()
		defer sess.networkMu.Unlock()

		if sess.networkActive {
			return fmt.Sprintf("Network capture already active on session %q", sess.Name), nil
		}

		recorder := newNetworkRecorder()
		sess.networkRecorder = recorder

		if err := (proto.NetworkEnable{}).Call(page); err != nil {
			sess.networkRecorder = nil
			return "", fmt.Errorf("playwright network: enable network events: %w", err)
		}

		capCtx, cancel := context.WithCancel(context.Background())
		sess.networkCancel = cancel
		sess.networkActive = true

		go page.Context(capCtx).EachEvent(
			func(e *proto.NetworkRequestWillBeSent) { recorder.requestWillBeSent(e) },
			func(e *proto.NetworkResponseReceived) { recorder.responseReceived(e) },
			func(e *proto.NetworkLoadingFinished) { recorder.loadingFinished(e) },
			func(e *proto.NetworkLoadingFailed) { recorder.loadingFailed(e) },
		)()

		return fmt.Sprintf("Network capture started on session %q", sess.Name), nil
	})
}

func networkCaptureDump(sess *Session) (string, error) {
	sess.networkMu.Lock()
	defer sess.networkMu.Unlock()

	if sess.networkRecorder == nil {
		return "No network capture active", nil
	}

	entries := sess.networkRecorder.snapshot()
	if len(entries) == 0 {
		return "No requests captured yet", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Captured %d request(s):\n", len(entries)))
	sb.WriteString(fmt.Sprintf("%-7s %-6s %-60s %-25s %s\n", "METHOD", "STATUS", "URL", "CONTENT-TYPE", "SIZE"))
	sb.WriteString(strings.Repeat("-", 120) + "\n")

	for _, e := range entries {
		displayURL := e.URL
		if len(displayURL) > 60 {
			displayURL = displayURL[:57] + "..."
		}
		ct := e.ContentType
		if idx := strings.Index(ct, ";"); idx > 0 {
			ct = ct[:idx]
		}
		sb.WriteString(fmt.Sprintf("%-7s %-6s %-60s %-25s %s\n",
			e.Method, strconv.Itoa(e.Status), displayURL, ct, strconv.Itoa(e.Size)))
	}
	return sb.String(), nil
}

func networkCaptureStop(ctx context.Context, sess *Session) (string, error) {
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		sess.networkMu.Lock()
		defer sess.networkMu.Unlock()

		if !sess.networkActive {
			return fmt.Sprintf("No network capture active on session %q", sess.Name), nil
		}

		if sess.networkCancel != nil {
			sess.networkCancel()
			sess.networkCancel = nil
		}
		_ = (proto.NetworkDisable{}).Call(page)
		sess.networkActive = false

		entries := sess.networkRecorder.snapshot()
		sess.networkRecorder = nil

		if len(entries) == 0 {
			return fmt.Sprintf("Network capture stopped on session %q (no requests captured)", sess.Name), nil
		}

		return fmt.Sprintf("Network capture stopped on session %q - captured %d request(s)", sess.Name, len(entries)), nil
	})
}

// ---------------------------------------------------------------------------
// press
// ---------------------------------------------------------------------------

var keyNameMap = map[string]input.Key{
	"enter": input.Enter, "tab": input.Tab, "escape": input.Escape,
	"backspace": input.Backspace, "delete": input.Delete, "space": input.Space,
	"arrowup": input.ArrowUp, "arrowdown": input.ArrowDown,
	"arrowleft": input.ArrowLeft, "arrowright": input.ArrowRight,
	"home": input.Home, "end": input.End,
	"pageup": input.PageUp, "pagedown": input.PageDown,
	"insert": input.Insert,
	"f1": input.F1, "f2": input.F2, "f3": input.F3, "f4": input.F4,
	"f5": input.F5, "f6": input.F6, "f7": input.F7, "f8": input.F8,
	"f9": input.F9, "f10": input.F10, "f11": input.F11, "f12": input.F12,
	"shift": input.ShiftLeft, "control": input.ControlLeft,
	"alt": input.AltLeft, "meta": input.MetaLeft,
}

func resolveKey(name string) (input.Key, error) {
	if k, ok := keyNameMap[strings.ToLower(name)]; ok {
		return k, nil
	}
	runes := []rune(name)
	if len(runes) == 1 {
		return input.Key(runes[0]), nil
	}
	return 0, fmt.Errorf("unknown key %q", name)
}

func (c *Command) execPress(ctx context.Context, args []string) (string, error) {
	if len(args) < 3 {
		return "", fmt.Errorf("playwright press: usage: playwright press <session> <selector> <key>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := args[1]
	keyExpr := strings.Join(args[2:], " ")

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright press: element %q not found: %w", selector, err)
		}
		if err := el.Focus(); err != nil {
			return "", fmt.Errorf("playwright press: focus %q: %w", selector, err)
		}

		parts := strings.Split(keyExpr, "+")
		if len(parts) == 1 {
			k, err := resolveKey(parts[0])
			if err != nil {
				return "", fmt.Errorf("playwright press: %w", err)
			}
			if err := page.Keyboard.Type(k); err != nil {
				return "", fmt.Errorf("playwright press: %w", err)
			}
		} else {
			ka := page.KeyActions()
			for _, p := range parts[:len(parts)-1] {
				mod, err := resolveKey(strings.TrimSpace(p))
				if err != nil {
					return "", fmt.Errorf("playwright press: modifier %w", err)
				}
				ka = ka.Press(mod)
			}
			main, err := resolveKey(strings.TrimSpace(parts[len(parts)-1]))
			if err != nil {
				return "", fmt.Errorf("playwright press: %w", err)
			}
			ka = ka.Type(main)
			for i := len(parts) - 2; i >= 0; i-- {
				mod, _ := resolveKey(strings.TrimSpace(parts[i]))
				ka = ka.Release(mod)
			}
			if err := ka.Do(); err != nil {
				return "", fmt.Errorf("playwright press: %w", err)
			}
		}

		return fmt.Sprintf("Pressed %q on %q", keyExpr, selector), nil
	})
}

// ---------------------------------------------------------------------------
// hover
// ---------------------------------------------------------------------------

func (c *Command) execHover(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright hover: usage: playwright hover <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright hover: element %q not found: %w", selector, err)
		}
		if err := el.Hover(); err != nil {
			return "", fmt.Errorf("playwright hover: %w", err)
		}
		return fmt.Sprintf("Hovered over %q", selector), nil
	})
}

// ---------------------------------------------------------------------------
// dblclick
// ---------------------------------------------------------------------------

func (c *Command) execDblclick(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright dblclick: usage: playwright dblclick <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright dblclick: element %q not found: %w", selector, err)
		}
		if err := el.Click(proto.InputMouseButtonLeft, 2); err != nil {
			return "", fmt.Errorf("playwright dblclick: %w", err)
		}
		_ = page.WaitStable(waitStableDur)
		return fmt.Sprintf("Double-clicked %q", selector), nil
	})
}

// ---------------------------------------------------------------------------
// get-attribute
// ---------------------------------------------------------------------------

func (c *Command) execGetAttribute(ctx context.Context, args []string) (string, error) {
	if len(args) < 3 {
		return "", fmt.Errorf("playwright get-attribute: usage: playwright get-attribute <session> <selector> <name>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := args[1]
	attrName := args[2]
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright get-attribute: element %q not found: %w", selector, err)
		}
		val, err := el.Attribute(attrName)
		if err != nil {
			return "", fmt.Errorf("playwright get-attribute: %w", err)
		}
		if val == nil {
			return fmt.Sprintf("%s @%s = null", selector, attrName), nil
		}
		return fmt.Sprintf("%s @%s = %s", selector, attrName, *val), nil
	})
}

// ---------------------------------------------------------------------------
// input-value
// ---------------------------------------------------------------------------

func (c *Command) execInputValue(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright input-value: usage: playwright input-value <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright input-value: element %q not found: %w", selector, err)
		}
		val, err := el.Property("value")
		if err != nil {
			return "", fmt.Errorf("playwright input-value: %w", err)
		}
		return fmt.Sprintf("%s value = %s", selector, val.Str()), nil
	})
}

// ---------------------------------------------------------------------------
// is-visible
// ---------------------------------------------------------------------------

func (c *Command) execIsVisible(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright is-visible: usage: playwright is-visible <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright is-visible: element %q not found: %w", selector, err)
		}
		visible, err := el.Visible()
		if err != nil {
			return "", fmt.Errorf("playwright is-visible: %w", err)
		}
		return fmt.Sprintf("%s visible = %v", selector, visible), nil
	})
}

// ---------------------------------------------------------------------------
// check / uncheck
// ---------------------------------------------------------------------------

func (c *Command) execCheck(ctx context.Context, args []string) (string, error) {
	return c.execSetChecked(ctx, args, true, "check")
}

func (c *Command) execUncheck(ctx context.Context, args []string) (string, error) {
	return c.execSetChecked(ctx, args, false, "uncheck")
}

func (c *Command) execSetChecked(ctx context.Context, args []string, want bool, cmd string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright %s: usage: playwright %s <session> <selector>", cmd, cmd)
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright %s: element %q not found: %w", cmd, selector, err)
		}
		checked, err := el.Property("checked")
		if err != nil {
			return "", fmt.Errorf("playwright %s: %w", cmd, err)
		}
		if checked.Bool() != want {
			if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
				return "", fmt.Errorf("playwright %s: %w", cmd, err)
			}
		}
		verb := "Checked"
		if !want {
			verb = "Unchecked"
		}
		return fmt.Sprintf("%s %q", verb, selector), nil
	})
}

// ---------------------------------------------------------------------------
// set-input-files
// ---------------------------------------------------------------------------

func (c *Command) execSetInputFiles(ctx context.Context, args []string) (string, error) {
	if len(args) < 3 {
		return "", fmt.Errorf("playwright set-input-files: usage: playwright set-input-files <session> <selector> <path...>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := args[1]
	paths := args[2:]

	resolved := make([]string, len(paths))
	for i, p := range paths {
		resolved[i] = resolvePath(c.workDir, p)
		if _, err := os.Stat(resolved[i]); err != nil {
			return "", fmt.Errorf("playwright set-input-files: file %q: %w", resolved[i], err)
		}
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright set-input-files: element %q not found: %w", selector, err)
		}
		if err := el.SetFiles(resolved); err != nil {
			return "", fmt.Errorf("playwright set-input-files: %w", err)
		}
		return fmt.Sprintf("Set %d file(s) on %q: %s", len(resolved), selector, strings.Join(paths, ", ")), nil
	})
}

// ---------------------------------------------------------------------------
// focus / blur
// ---------------------------------------------------------------------------

func (c *Command) execFocus(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright focus: usage: playwright focus <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright focus: element %q not found: %w", selector, err)
		}
		if err := el.Focus(); err != nil {
			return "", fmt.Errorf("playwright focus: %w", err)
		}
		return fmt.Sprintf("Focused %q", selector), nil
	})
}

func (c *Command) execBlur(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright blur: usage: playwright blur <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright blur: element %q not found: %w", selector, err)
		}
		if err := el.Blur(); err != nil {
			return "", fmt.Errorf("playwright blur: %w", err)
		}
		return fmt.Sprintf("Blurred %q", selector), nil
	})
}

// ---------------------------------------------------------------------------
// is-hidden / is-checked / is-disabled / is-enabled
// ---------------------------------------------------------------------------

func (c *Command) execIsHidden(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright is-hidden: usage: playwright is-hidden <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright is-hidden: element %q not found: %w", selector, err)
		}
		visible, err := el.Visible()
		if err != nil {
			return "", fmt.Errorf("playwright is-hidden: %w", err)
		}
		return fmt.Sprintf("%s hidden = %v", selector, !visible), nil
	})
}

func (c *Command) execIsChecked(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright is-checked: usage: playwright is-checked <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright is-checked: element %q not found: %w", selector, err)
		}
		val, err := el.Property("checked")
		if err != nil {
			return "", fmt.Errorf("playwright is-checked: %w", err)
		}
		return fmt.Sprintf("%s checked = %v", selector, val.Bool()), nil
	})
}

func (c *Command) execIsDisabled(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright is-disabled: usage: playwright is-disabled <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright is-disabled: element %q not found: %w", selector, err)
		}
		disabled, err := el.Disabled()
		if err != nil {
			return "", fmt.Errorf("playwright is-disabled: %w", err)
		}
		return fmt.Sprintf("%s disabled = %v", selector, disabled), nil
	})
}

func (c *Command) execIsEnabled(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright is-enabled: usage: playwright is-enabled <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright is-enabled: element %q not found: %w", selector, err)
		}
		disabled, err := el.Disabled()
		if err != nil {
			return "", fmt.Errorf("playwright is-enabled: %w", err)
		}
		return fmt.Sprintf("%s enabled = %v", selector, !disabled), nil
	})
}

// ---------------------------------------------------------------------------
// inner-text
// ---------------------------------------------------------------------------

func (c *Command) execInnerText(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright inner-text: usage: playwright inner-text <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright inner-text: element %q not found: %w", selector, err)
		}
		text, err := el.Text()
		if err != nil {
			return "", fmt.Errorf("playwright inner-text: %w", err)
		}
		return text, nil
	})
}

// ---------------------------------------------------------------------------
// tap
// ---------------------------------------------------------------------------

func (c *Command) execTap(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright tap: usage: playwright tap <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright tap: element %q not found: %w", selector, err)
		}
		if err := el.Tap(); err != nil {
			return "", fmt.Errorf("playwright tap: %w", err)
		}
		return fmt.Sprintf("Tapped %q", selector), nil
	})
}

// ---------------------------------------------------------------------------
// type (character-by-character input)
// ---------------------------------------------------------------------------

func (c *Command) execType(ctx context.Context, args []string) (string, error) {
	if len(args) < 3 {
		return "", fmt.Errorf("playwright type: usage: playwright type <session> <selector> <text>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := args[1]
	text := strings.Join(args[2:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright type: element %q not found: %w", selector, err)
		}
		for _, r := range text {
			if err := el.Type(input.Key(r)); err != nil {
				return "", fmt.Errorf("playwright type: %w", err)
			}
		}
		return fmt.Sprintf("Typed %d chars into %q", len([]rune(text)), selector), nil
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func findElement(page *rod.Page, selector string) (*rod.Element, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, fmt.Errorf("empty selector")
	}
	if xpath, ok := strings.CutPrefix(selector, "xpath:"); ok {
		return page.ElementX(xpath)
	}
	return page.Element(selector)
}

func selectOption(el *rod.Element, value string) error {
	if err := el.Select([]string{value}, true, rod.SelectorTypeText); err == nil {
		return nil
	}
	return el.Select([]string{fmt.Sprintf("option[value=%s]", strconv.Quote(value))}, true, rod.SelectorTypeCSSSector)
}
