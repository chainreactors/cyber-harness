package proxy

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/chainreactors/proxyclient"
	"github.com/chainreactors/proxyclient/extra/clash"
)

type OnProxyChange func(newProxyURL string)

type Command struct {
	state         *State
	onProxyChange OnProxyChange
}

func New(state *State) *Command {
	return &Command{state: state}
}

func (c *Command) SetOnProxyChange(fn OnProxyChange) {
	c.onProxyChange = fn
}

func (c *Command) Name() string { return "proxy" }

func (c *Command) Usage() string {
	return `proxy - Manage proxy nodes from Clash subscriptions

Usage:
  proxy auto <url> [options]  Auto mode: subscribe + adaptive load balancing (recommended)
  proxy subscribe <url>       Fetch a Clash subscription and list available nodes
  proxy list                  List loaded proxy nodes
  proxy switch <name|index>   Switch the active proxy node (single node)
  proxy test [name|index]     Test proxy node connectivity
  proxy current               Show the current active proxy
  proxy clear                 Clear subscription and revert to original proxy

Auto mode options:
  --type,-t  trojan,vless     Filter by protocol type
  --name,-n  keyword          Filter by node name keyword
  --country,-c HK,JP,US      Filter by server IP country (ISO 3166-1 alpha-2)
  --strategy,-s adaptive      Load balance strategy (adaptive, url-test, round-robin, random)`
}

func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return c.Usage(), nil
	}
	subcmd := strings.ToLower(args[0])
	rest := args[1:]

	switch subcmd {
	case "auto":
		return c.execAuto(ctx, rest)
	case "subscribe", "sub":
		return c.execSubscribe(ctx, rest)
	case "list", "ls":
		return c.execList()
	case "switch", "sw":
		return c.execSwitch(rest)
	case "test":
		return c.execTest(ctx, rest)
	case "current":
		return c.execCurrent()
	case "clear":
		return c.execClear()
	default:
		return "", fmt.Errorf("unknown proxy subcommand: %s\n%s", subcmd, c.Usage())
	}
}

func (c *Command) execAuto(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: proxy auto <subscription-url> [--type trojan,vless] [--name keyword] [--country HK,JP] [--strategy adaptive]")
	}
	subURL := args[0]
	strategy := "adaptive"
	var typeFilter, nameFilter, countryFilter string

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--type", "-t":
			if i+1 < len(args) { typeFilter = args[i+1]; i++ }
		case "--name", "-n":
			if i+1 < len(args) { nameFilter = args[i+1]; i++ }
		case "--country", "-c":
			if i+1 < len(args) { countryFilter = args[i+1]; i++ }
		case "--strategy", "-s":
			if i+1 < len(args) { strategy = args[i+1]; i++ }
		}
	}

	q := url.Values{}
	q.Set("url", subURL)
	q.Set("strategy", strategy)
	q.Set("ua", "clash-verge/v2.0.0")
	if typeFilter != "" {
		q.Set("type", typeFilter)
	}
	if nameFilter != "" {
		q.Set("name", nameFilter)
	}
	if countryFilter != "" {
		q.Set("country", countryFilter)
	}
	clashURL := "clash://?" + q.Encode()

	// also load subscription for list/switch commands
	sub, err := clash.FetchSubscriptionWithUA(subURL, "clash-verge/v2.0.0")
	if err != nil {
		return "", fmt.Errorf("fetch subscription: %w", err)
	}
	c.state.LoadSubscription(sub, subURL)

	// create the clash:// dialer
	u, _ := url.Parse(clashURL)
	dial, err := proxyclient.NewClient(u)
	if err != nil {
		return "", fmt.Errorf("create dialer: %w", err)
	}

	// store as active proxy
	c.state.SetAutoDial(clashURL, dial)

	if c.onProxyChange != nil {
		c.onProxyChange(clashURL)
	}

	supported := clash.SupportedNodes(sub)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[proxy] auto mode enabled\n"))
	sb.WriteString(fmt.Sprintf("  Subscription: %s\n", subURL))
	sb.WriteString(fmt.Sprintf("  Nodes: %d total, %d supported\n", len(sub.Nodes), len(supported)))
	sb.WriteString(fmt.Sprintf("  Strategy: %s\n", strategy))
	if typeFilter != "" {
		sb.WriteString(fmt.Sprintf("  Type filter: %s\n", typeFilter))
	}
	if nameFilter != "" {
		sb.WriteString(fmt.Sprintf("  Name filter: %s\n", nameFilter))
	}
	if countryFilter != "" {
		sb.WriteString(fmt.Sprintf("  Country filter: %s\n", countryFilter))
	}
	sb.WriteString("  All traffic will auto-route through healthy nodes.")
	return sb.String(), nil
}

func (c *Command) execSubscribe(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: proxy subscribe <clash-subscription-url>")
	}
	subURL := args[0]

	sub, err := clash.FetchSubscriptionWithUA(subURL, "clash-verge/v2.0.0")
	if err != nil {
		return "", fmt.Errorf("fetch subscription: %w", err)
	}
	c.state.LoadSubscription(sub, subURL)

	return c.formatNodeList(sub.Nodes, ""), nil
}

func (c *Command) execList() (string, error) {
	nodes := c.state.Nodes()
	if len(nodes) == 0 {
		return "[proxy] no subscription loaded. Use: proxy subscribe <url>", nil
	}
	activeName := c.state.ActiveNodeName()
	return c.formatNodeList(nodes, activeName), nil
}

func (c *Command) execSwitch(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: proxy switch <node-name|index>")
	}
	nameOrIndex := strings.Join(args, " ")
	if err := c.state.Switch(nameOrIndex); err != nil {
		return "", err
	}

	newProxy := c.state.ActiveProxy()
	if c.onProxyChange != nil {
		c.onProxyChange(newProxy)
	}

	nodeName := c.state.ActiveNodeName()
	return fmt.Sprintf("[proxy] switched to %q\nProxy URL: %s", nodeName, newProxy), nil
}

func (c *Command) execTest(ctx context.Context, args []string) (string, error) {
	nodes := c.state.Nodes()
	if len(nodes) == 0 {
		return "", fmt.Errorf("no subscription loaded")
	}

	// if no arg, test all supported nodes
	if len(args) == 0 {
		var sb strings.Builder
		sb.WriteString("[proxy] testing all supported nodes...\n")
		for i, node := range nodes {
			if !node.Supported {
				continue
			}
			latency, err := c.state.TestNode(ctx, &nodes[i])
			if err != nil {
				sb.WriteString(fmt.Sprintf("  %d. %-20s %-10s FAIL (%v)\n", i+1, node.Name, node.Type, err))
			} else {
				sb.WriteString(fmt.Sprintf("  %d. %-20s %-10s %dms\n", i+1, node.Name, node.Type, latency.Milliseconds()))
			}
		}
		return sb.String(), nil
	}

	// find specific node
	nameOrIndex := strings.Join(args, " ")
	node, idx, err := c.findNode(nameOrIndex)
	if err != nil {
		return "", err
	}
	latency, testErr := c.state.TestNode(ctx, node)
	if testErr != nil {
		return fmt.Sprintf("[proxy] test %d. %s (%s): FAIL (%v)", idx+1, node.Name, node.Type, testErr), nil
	}
	return fmt.Sprintf("[proxy] test %d. %s (%s): %dms", idx+1, node.Name, node.Type, latency.Milliseconds()), nil
}

func (c *Command) execCurrent() (string, error) {
	if c.state.IsAutoMode() {
		proxy := c.state.ActiveProxy()
		return fmt.Sprintf("[proxy] auto mode (adaptive load balancing)\nClash URL: %s", proxy), nil
	}
	nodeName := c.state.ActiveNodeName()
	proxy := c.state.ActiveProxy()
	if nodeName != "" {
		return fmt.Sprintf("[proxy] active node: %s\nProxy URL: %s", nodeName, proxy), nil
	}
	if proxy != "" {
		return fmt.Sprintf("[proxy] using original proxy: %s", proxy), nil
	}
	return "[proxy] no proxy configured", nil
}

func (c *Command) execClear() (string, error) {
	c.state.Clear()
	original := c.state.OriginalProxy()
	if c.onProxyChange != nil {
		c.onProxyChange(original)
	}
	if original != "" {
		return fmt.Sprintf("[proxy] cleared. Reverted to original proxy: %s", original), nil
	}
	return "[proxy] cleared. No proxy active.", nil
}

func (c *Command) findNode(nameOrIndex string) (*clash.ProxyNode, int, error) {
	nodes := c.state.Nodes()
	// try as index
	if idx, err := fmt.Sscanf(nameOrIndex, "%d", new(int)); err == nil && idx == 1 {
		var i int
		fmt.Sscanf(nameOrIndex, "%d", &i)
		if i < 1 || i > len(nodes) {
			return nil, 0, fmt.Errorf("index %d out of range (1-%d)", i, len(nodes))
		}
		return &nodes[i-1], i - 1, nil
	}
	// try as name
	lower := strings.ToLower(nameOrIndex)
	for i := range nodes {
		if strings.ToLower(nodes[i].Name) == lower {
			return &nodes[i], i, nil
		}
	}
	return nil, 0, fmt.Errorf("node %q not found", nameOrIndex)
}

func (c *Command) formatNodeList(nodes []clash.ProxyNode, activeName string) string {
	var sb strings.Builder
	supported := 0
	typeCount := map[string]int{}
	for _, n := range nodes {
		if n.Supported {
			supported++
			typeCount[n.Type]++
		}
	}
	var typeSummary []string
	for t, c := range typeCount {
		typeSummary = append(typeSummary, fmt.Sprintf("%s:%d", t, c))
	}
	sb.WriteString(fmt.Sprintf("[proxy] %d nodes (%d supported: %s)\n", len(nodes), supported, strings.Join(typeSummary, ", ")))
	sb.WriteString(fmt.Sprintf("  %-4s %-24s %-10s %-30s %s\n", "#", "Name", "Type", "Server", "Status"))
	sb.WriteString(fmt.Sprintf("  %-4s %-24s %-10s %-30s %s\n", "---", "---", "---", "---", "---"))
	for i, node := range nodes {
		status := "ok"
		if !node.Supported {
			status = "unsupported"
		}
		if activeName != "" && node.Name == activeName {
			status = "* active"
		}
		server := node.Server
		if node.Port > 0 {
			server = fmt.Sprintf("%s:%d", node.Server, node.Port)
		}
		sb.WriteString(fmt.Sprintf("  %-4d %-24s %-10s %-30s %s\n", i+1, truncate(node.Name, 24), node.Type, truncate(server, 30), status))
	}
	return sb.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
