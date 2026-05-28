//go:build browser

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
)

// ---------------------------------------------------------------------------
// click
// ---------------------------------------------------------------------------

func (c *Command) execClick(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("browser click: usage: browser click <session> <selector>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := strings.Join(args[1:], " ")

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("browser click: element %q not found: %w", selector, err)
		}
		if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return "", fmt.Errorf("browser click: %w", err)
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
		return "", fmt.Errorf("browser fill: usage: browser fill <session> <selector> <value>")
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
			return "", fmt.Errorf("browser fill: element %q not found: %w", selector, err)
		}
		_ = el.SelectAllText()
		if err := el.Input(value); err != nil {
			return "", fmt.Errorf("browser fill: %w", err)
		}
		return fmt.Sprintf("Filled %q with %q", selector, value), nil
	})
}

// ---------------------------------------------------------------------------
// select
// ---------------------------------------------------------------------------

func (c *Command) execSelect(ctx context.Context, args []string) (string, error) {
	if len(args) < 3 {
		return "", fmt.Errorf("browser select: usage: browser select <session> <selector> <value>")
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
			return "", fmt.Errorf("browser select: element %q not found: %w", selector, err)
		}
		if err := selectOption(el, value); err != nil {
			return "", fmt.Errorf("browser select: %w", err)
		}
		return fmt.Sprintf("Selected %q in %q", value, selector), nil
	})
}

// ---------------------------------------------------------------------------
// wait
// ---------------------------------------------------------------------------

func (c *Command) execWait(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("browser wait: usage: browser wait <session> <selector|--idle|--stable>")
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
				return "", fmt.Errorf("browser wait: element %q did not appear: %w", target, err)
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
		return "", fmt.Errorf("browser %s: session name required", commandName)
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
			return "", fmt.Errorf("browser %s: element %q not found: %w", commandName, selector, err)
		}
		text, err := el.Text()
		if err != nil {
			return "", fmt.Errorf("browser %s: %w", commandName, err)
		}
		return formatTextOutput(selector, text), nil
	})
}

// ---------------------------------------------------------------------------
// session HTML extraction
// ---------------------------------------------------------------------------

func (c *Command) execSessionContent(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("browser content: session name required")
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
				return "", fmt.Errorf("browser content: element %q not found: %w", selector, err)
			}
			html, err := el.HTML()
			if err != nil {
				return "", fmt.Errorf("browser content: %w", err)
			}
			return formatHTMLOutput(selector, html), nil
		}

		html, err := page.HTML()
		if err != nil {
			return "", fmt.Errorf("browser content: %w", err)
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
		return "", fmt.Errorf("browser eval: usage: browser eval <url|session> <script>")
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
			return "", fmt.Errorf("browser eval: %w", err)
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
		return "", fmt.Errorf("browser screenshot: session name required")
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
				return "", fmt.Errorf("browser screenshot: --output requires a value")
			}
		case "--selector":
			if i+1 < len(args) {
				i++
				selector = args[i]
			} else {
				return "", fmt.Errorf("browser screenshot: --selector requires a value")
			}
		case "--full-page":
			fullPage = true
		default:
			return "", fmt.Errorf("browser screenshot: unknown flag: %s", args[i])
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
				return "", fmt.Errorf("browser screenshot: element %q not found: %w", selector, elErr)
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
			return "", fmt.Errorf("browser screenshot: capture: %w", err)
		}

		if err := writeFile(outPath, data); err != nil {
			return "", fmt.Errorf("browser screenshot: write: %w", err)
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
		return "", fmt.Errorf("browser url: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		info, err := page.Info()
		if err != nil {
			return "", fmt.Errorf("browser url: %w", err)
		}
		return fmt.Sprintf("URL: %s\nTitle: %s", info.URL, info.Title), nil
	})
}

// ---------------------------------------------------------------------------
// cookies: list / set / clear
// ---------------------------------------------------------------------------

func (c *Command) execCookies(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("browser cookies: usage: browser cookies <session> --list|--set k=v|--clear")
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
				return "", fmt.Errorf("browser cookies: %w", err)
			}
			if len(cookies) == 0 {
				return "No cookies", nil
			}
			data, _ := json.MarshalIndent(cookies, "", "  ")
			return fmt.Sprintf("Cookies (%d):\n%s", len(cookies), string(data)), nil

		case "--set":
			if len(args) < 3 {
				return "", fmt.Errorf("browser cookies --set: requires name=value")
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
				return "", fmt.Errorf("browser cookies --set: no valid name=value pairs")
			}
			if err := page.SetCookies(cookies); err != nil {
				return "", fmt.Errorf("browser cookies --set: %w", err)
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
			return "", fmt.Errorf("browser cookies: unknown flag %q (expected --list, --set, or --clear)", flag)
		}
	})
}

// ---------------------------------------------------------------------------
// session network capture (start/dump/stop)
// ---------------------------------------------------------------------------

func (c *Command) execSessionNetwork(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("browser network: usage: browser network <url|session> --start|--dump|--stop")
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
		return "", fmt.Errorf("browser network: unknown flag %q", flag)
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
			return "", fmt.Errorf("browser network: enable network events: %w", err)
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
