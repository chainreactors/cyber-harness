//go:build browser

package playwright

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ---------------------------------------------------------------------------
// set-extra-headers
// ---------------------------------------------------------------------------

func (c *Command) execSetExtraHeaders(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright set-extra-headers: usage: playwright set-extra-headers <session> <json>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	raw := strings.Join(args[1:], " ")
	var headers map[string]string
	if err := json.Unmarshal([]byte(raw), &headers); err != nil {
		return "", fmt.Errorf("playwright set-extra-headers: invalid JSON: %w", err)
	}
	flat := make([]string, 0, len(headers)*2)
	names := make([]string, 0, len(headers))
	for k, v := range headers {
		flat = append(flat, k, v)
		names = append(names, k)
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		sess.headerMu.Lock()
		if sess.headerCleanup != nil {
			sess.headerCleanup()
		}
		sess.headerMu.Unlock()

		cleanup, err := page.SetExtraHeaders(flat)
		if err != nil {
			return "", fmt.Errorf("playwright set-extra-headers: %w", err)
		}
		sess.headerMu.Lock()
		sess.headerCleanup = cleanup
		sess.headerMu.Unlock()

		return fmt.Sprintf("Set %d extra header(s): %s", len(headers), strings.Join(names, ", ")), nil
	})
}

// ---------------------------------------------------------------------------
// set-viewport
// ---------------------------------------------------------------------------

func (c *Command) execSetViewport(ctx context.Context, args []string) (string, error) {
	if len(args) < 3 {
		return "", fmt.Errorf("playwright set-viewport: usage: playwright set-viewport <session> <width> <height>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	w, err := strconv.Atoi(args[1])
	if err != nil || w <= 0 {
		return "", fmt.Errorf("playwright set-viewport: width must be a positive integer")
	}
	h, err := strconv.Atoi(args[2])
	if err != nil || h <= 0 {
		return "", fmt.Errorf("playwright set-viewport: height must be a positive integer")
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
			Width: w, Height: h, DeviceScaleFactor: 1,
		}); err != nil {
			return "", fmt.Errorf("playwright set-viewport: %w", err)
		}
		return fmt.Sprintf("Viewport set to %dx%d", w, h), nil
	})
}

// ---------------------------------------------------------------------------
// dispatch-event
// ---------------------------------------------------------------------------

func (c *Command) execDispatchEvent(ctx context.Context, args []string) (string, error) {
	if len(args) < 3 {
		return "", fmt.Errorf("playwright dispatch-event: usage: playwright dispatch-event <session> <selector> <event-type>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	selector := args[1]
	eventType := args[2]

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		el, err := findElement(page, selector)
		if err != nil {
			return "", fmt.Errorf("playwright dispatch-event: element %q not found: %w", selector, err)
		}
		_, err = el.Eval("(eventType) => { this.dispatchEvent(new Event(eventType, {bubbles: true})); }", eventType)
		if err != nil {
			return "", fmt.Errorf("playwright dispatch-event: %w", err)
		}
		return fmt.Sprintf("Dispatched %q on %q", eventType, selector), nil
	})
}

// ---------------------------------------------------------------------------
// route / unroute
// ---------------------------------------------------------------------------

func (c *Command) execRoute(ctx context.Context, args []string) (string, error) {
	if len(args) < 3 {
		return "", fmt.Errorf("playwright route: usage: playwright route <session> <url-pattern> --fulfill|--abort|--continue [options]")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	pattern := args[1]
	mode, opts, err := parseRouteOpts(args[2:])
	if err != nil {
		return "", err
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		sess.hijackMu.Lock()
		if sess.hijackRouter == nil {
			sess.hijackRouter = page.HijackRequests()
		}
		router := sess.hijackRouter
		sess.hijackMu.Unlock()

		switch mode {
		case "fulfill":
			router.MustAdd(pattern, func(ctx *rod.Hijack) {
				for k, v := range opts.headers {
					ctx.Response.SetHeader(k, v)
				}
				if opts.contentType != "" {
					ctx.Response.SetHeader("Content-Type", opts.contentType)
				}
				ctx.Response.SetBody(opts.body)
				ctx.Response.Payload().ResponseCode = opts.status
			})
		case "abort":
			router.MustAdd(pattern, func(ctx *rod.Hijack) {
				ctx.Response.Fail(proto.NetworkErrorReasonAborted)
			})
		case "continue":
			router.MustAdd(pattern, func(ctx *rod.Hijack) {
				for k, v := range opts.headers {
					ctx.Request.Req().Header.Set(k, v)
				}
				ctx.ContinueRequest(&proto.FetchContinueRequest{})
			})
		}

		sess.hijackMu.Lock()
		if !sess.hijackRunning {
			sess.hijackRunning = true
			go router.Run()
		}
		sess.hijackMu.Unlock()

		return fmt.Sprintf("Route set: %s -> %s", pattern, mode), nil
	})
}

type routeOpts struct {
	status      int
	body        string
	contentType string
	headers     map[string]string
}

func parseRouteOpts(args []string) (string, routeOpts, error) {
	opts := routeOpts{status: 200, headers: map[string]string{}}
	mode := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--fulfill":
			mode = "fulfill"
		case "--abort":
			mode = "abort"
		case "--continue":
			mode = "continue"
		case "--status":
			if i+1 >= len(args) {
				return "", opts, fmt.Errorf("playwright route: --status requires a value")
			}
			i++
			s, err := strconv.Atoi(args[i])
			if err != nil {
				return "", opts, fmt.Errorf("playwright route: --status must be an integer: %w", err)
			}
			opts.status = s
		case "--body":
			if i+1 >= len(args) {
				return "", opts, fmt.Errorf("playwright route: --body requires a value")
			}
			i++
			opts.body = args[i]
		case "--content-type":
			if i+1 >= len(args) {
				return "", opts, fmt.Errorf("playwright route: --content-type requires a value")
			}
			i++
			opts.contentType = args[i]
		case "--header":
			if i+1 >= len(args) {
				return "", opts, fmt.Errorf("playwright route: --header requires key=value")
			}
			i++
			k, v, ok := strings.Cut(args[i], "=")
			if !ok {
				return "", opts, fmt.Errorf("playwright route: --header must be key=value, got %q", args[i])
			}
			opts.headers[k] = v
		default:
			return "", opts, fmt.Errorf("playwright route: unknown flag %q", args[i])
		}
	}
	if mode == "" {
		return "", opts, fmt.Errorf("playwright route: must specify --fulfill, --abort, or --continue")
	}
	return mode, opts, nil
}

func (c *Command) execUnroute(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright unroute: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		sess.hijackMu.Lock()
		defer sess.hijackMu.Unlock()
		if sess.hijackRouter != nil {
			_ = sess.hijackRouter.Stop()
			sess.hijackRouter = nil
			sess.hijackRunning = false
		}
		return "All routes removed", nil
	})
}

// ---------------------------------------------------------------------------
// wait-for-url
// ---------------------------------------------------------------------------

func (c *Command) execWaitForURL(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright wait-for-url: usage: playwright wait-for-url <session> <url-substring>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	pattern := args[1]
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		wait := page.EachEvent(func(e *proto.PageFrameNavigated) bool {
			return strings.Contains(e.Frame.URL, pattern)
		})
		wait()
		info, _ := page.Info()
		url := ""
		if info != nil {
			url = info.URL
		}
		return fmt.Sprintf("URL matched: %s", url), nil
	})
}

// ---------------------------------------------------------------------------
// wait-for-request / wait-for-response
// ---------------------------------------------------------------------------

func (c *Command) execWaitForRequest(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright wait-for-request: usage: playwright wait-for-request <session> <url-substring>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	pattern := args[1]
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		var matched string
		wait := page.EachEvent(func(e *proto.NetworkRequestWillBeSent) bool {
			if strings.Contains(e.Request.URL, pattern) {
				matched = e.Request.Method + " " + e.Request.URL
				return true
			}
			return false
		})
		wait()
		return fmt.Sprintf("Request matched: %s", matched), nil
	})
}

func (c *Command) execWaitForResponse(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright wait-for-response: usage: playwright wait-for-response <session> <url-substring>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	pattern := args[1]
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		var matched string
		wait := page.EachEvent(func(e *proto.NetworkResponseReceived) bool {
			if strings.Contains(e.Response.URL, pattern) {
				matched = fmt.Sprintf("%d %s", e.Response.Status, e.Response.URL)
				return true
			}
			return false
		})
		wait()
		return fmt.Sprintf("Response matched: %s", matched), nil
	})
}
