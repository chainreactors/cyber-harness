//go:build full

package playwright

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent/truncate"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
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

		// Enable Network domain and set headers via CDP directly.
		if err := (proto.NetworkEnable{}).Call(page); err != nil {
			return "", fmt.Errorf("playwright set-extra-headers: enable network: %w", err)
		}
		hdrs := proto.NetworkHeaders{}
		for k, v := range headers {
			hdrs[k] = gson.New(v)
		}
		if err := (proto.NetworkSetExtraHTTPHeaders{Headers: hdrs}).Call(page); err != nil {
			return "", fmt.Errorf("playwright set-extra-headers: %w", err)
		}

		cleanup := func() {
			_ = (proto.NetworkDisable{}).Call(sess.Page)
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

	// Create the hijack router on the persistent session page (not the
	// timeout-scoped page from withPage) so it outlives individual operations.
	sess.hijackMu.Lock()
	if sess.hijackRouter == nil {
		sess.hijackRouter = sess.Page.HijackRequests()
	}
	router := sess.hijackRouter
	sess.hijackMu.Unlock()

	switch mode {
	case "fulfill":
		router.MustAdd(pattern, func(h *rod.Hijack) {
			for k, v := range opts.headers {
				h.Response.SetHeader(k, v)
			}
			if opts.contentType != "" {
				h.Response.SetHeader("Content-Type", opts.contentType)
			}
			h.Response.SetBody(opts.body)
			h.Response.Payload().ResponseCode = opts.status
		})
	case "abort":
		router.MustAdd(pattern, func(h *rod.Hijack) {
			h.Response.Fail(proto.NetworkErrorReasonAborted)
		})
	case "continue":
		router.MustAdd(pattern, func(h *rod.Hijack) {
			for k, v := range opts.headers {
				h.Request.Req().Header.Set(k, v)
			}
			h.ContinueRequest(&proto.FetchContinueRequest{})
		})
	}

	sess.hijackMu.Lock()
	sess.routeEntries = append(sess.routeEntries, routeEntry{Pattern: pattern, Mode: mode})
	if !sess.hijackRunning {
		sess.hijackRunning = true
		go router.Run()
	}
	sess.hijackMu.Unlock()

	return fmt.Sprintf("Route set: %s -> %s", pattern, mode), nil
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
		sess.routeEntries = nil
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
		// Check current URL first, then listen for navigations.
		if info, _ := page.Info(); info != nil && strings.Contains(info.URL, pattern) {
			return fmt.Sprintf("URL matched: %s", info.URL), nil
		}
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
		_ = (proto.NetworkEnable{}).Call(page)
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
		_ = (proto.NetworkEnable{}).Call(page)
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

// ---------------------------------------------------------------------------
// route-list
// ---------------------------------------------------------------------------

func (c *Command) execRouteList(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright route-list: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	sess.hijackMu.Lock()
	entries := make([]routeEntry, len(sess.routeEntries))
	copy(entries, sess.routeEntries)
	sess.hijackMu.Unlock()

	if len(entries) == 0 {
		return "No active routes", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Active routes (%d):\n", len(entries)))
	for i, r := range entries {
		sb.WriteString(fmt.Sprintf("  [%d] %s -> %s\n", i, r.Pattern, r.Mode))
	}
	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// requests — list all captured network requests
// ---------------------------------------------------------------------------

func (c *Command) execRequests(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright requests: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	sess.networkMu.Lock()
	rec := sess.networkRecorder
	sess.networkMu.Unlock()

	if rec == nil {
		return "No requests captured (network capture not active)", nil
	}

	entries := rec.snapshot()
	if len(entries) == 0 {
		return "No requests captured yet", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Requests (%d):\n", len(entries)))
	sb.WriteString(fmt.Sprintf("%-5s %-7s %-6s %-60s %-25s %s\n", "#", "METHOD", "STATUS", "URL", "CONTENT-TYPE", "SIZE"))
	sb.WriteString(strings.Repeat("-", 120) + "\n")

	for i, e := range entries {
		displayURL := e.URL
		if len(displayURL) > 60 {
			displayURL = displayURL[:57] + "..."
		}
		ct := e.ContentType
		if idx := strings.Index(ct, ";"); idx > 0 {
			ct = ct[:idx]
		}
		sb.WriteString(fmt.Sprintf("%-5d %-7s %-6s %-60s %-25s %s\n",
			i, e.Method, strconv.Itoa(e.Status), displayURL, ct, strconv.Itoa(e.Size)))
	}
	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// request <index> — show detail for a specific captured request
// ---------------------------------------------------------------------------

func (c *Command) execRequestDetail(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright request: usage: playwright request <session> <index>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	idx, err := strconv.Atoi(args[1])
	if err != nil {
		return "", fmt.Errorf("playwright request: index must be an integer: %w", err)
	}

	sess.networkMu.Lock()
	rec := sess.networkRecorder
	sess.networkMu.Unlock()

	if rec == nil {
		return "", fmt.Errorf("playwright request: no requests captured")
	}

	entries := rec.snapshot()
	if idx < 0 || idx >= len(entries) {
		return "", fmt.Errorf("playwright request: index %d out of range (0-%d)", idx, len(entries)-1)
	}

	e := entries[idx]
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Request #%d:\n", idx))
	sb.WriteString(fmt.Sprintf("  Method:       %s\n", e.Method))
	sb.WriteString(fmt.Sprintf("  URL:          %s\n", e.URL))
	sb.WriteString(fmt.Sprintf("  Status:       %d\n", e.Status))
	sb.WriteString(fmt.Sprintf("  Content-Type: %s\n", e.ContentType))
	sb.WriteString(fmt.Sprintf("  Size:         %d bytes\n", e.Size))

	if len(e.ReqHeaders) > 0 {
		sb.WriteString("\n  Request Headers:\n")
		for k, v := range e.ReqHeaders {
			sb.WriteString(fmt.Sprintf("    %s: %s\n", k, v))
		}
	}
	if e.PostData != "" {
		sb.WriteString(fmt.Sprintf("\n  Post Data:\n    %s\n", e.PostData))
	}
	if len(e.RespHeaders) > 0 {
		sb.WriteString("\n  Response Headers:\n")
		for k, v := range e.RespHeaders {
			sb.WriteString(fmt.Sprintf("    %s: %s\n", k, v))
		}
	}

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// snapshot — accessibility tree
// ---------------------------------------------------------------------------

func (c *Command) execSnapshot(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright snapshot: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	depth := 0
	for i := 1; i < len(args); i++ {
		if args[i] == "--depth" && i+1 < len(args) {
			i++
			d, parseErr := strconv.Atoi(args[i])
			if parseErr != nil {
				return "", fmt.Errorf("playwright snapshot: --depth must be integer: %w", parseErr)
			}
			depth = d
		}
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		req := proto.AccessibilityGetFullAXTree{}
		if depth > 0 {
			req.Depth = &depth
		}
		res, err := req.Call(page)
		if err != nil {
			return "", fmt.Errorf("playwright snapshot: %w", err)
		}
		return formatAXTree(res.Nodes), nil
	})
}

func formatAXTree(nodes []*proto.AccessibilityAXNode) string {
	if len(nodes) == 0 {
		return "Empty accessibility tree"
	}

	children := make(map[proto.AccessibilityAXNodeID][]proto.AccessibilityAXNodeID)
	nodeMap := make(map[proto.AccessibilityAXNodeID]*proto.AccessibilityAXNode)
	var rootID proto.AccessibilityAXNodeID

	for _, n := range nodes {
		nodeMap[n.NodeID] = n
		if n.ParentID == "" {
			rootID = n.NodeID
		}
		for _, childID := range n.ChildIDs {
			children[n.NodeID] = append(children[n.NodeID], childID)
		}
	}

	var sb strings.Builder
	var walk func(id proto.AccessibilityAXNodeID, indent int)
	walk = func(id proto.AccessibilityAXNodeID, indent int) {
		n, ok := nodeMap[id]
		if !ok {
			return
		}
		if n.Ignored {
			for _, childID := range children[id] {
				walk(childID, indent)
			}
			return
		}

		prefix := strings.Repeat("  ", indent)
		role := ""
		if n.Role != nil {
			role = n.Role.Value.Str()
		}
		name := ""
		if n.Name != nil && !n.Name.Value.Nil() {
			name = n.Name.Value.Str()
		}

		line := prefix + "- " + role
		if name != "" {
			line += fmt.Sprintf(" %q", name)
		}

		for _, prop := range n.Properties {
			pn := string(prop.Name)
			if pn == "level" || pn == "checked" || pn == "disabled" || pn == "required" || pn == "expanded" || pn == "selected" {
				line += fmt.Sprintf(" [%s=%s]", pn, prop.Value.Value.Str())
			}
		}

		sb.WriteString(line + "\n")
		for _, childID := range children[id] {
			walk(childID, indent+1)
		}
	}

	if rootID != "" {
		walk(rootID, 0)
	} else if len(nodes) > 0 {
		walk(nodes[0].NodeID, 0)
	}

	result := sb.String()
	tr := truncate.Head(result, truncate.Options{MaxBytes: maxOutputLen})
	if tr.Truncated {
		return tr.Content + fmt.Sprintf("\n\n[Snapshot truncated: showing %d/%d lines. Use --depth to limit.]",
			tr.OutputLines, tr.TotalLines)
	}
	return result
}
