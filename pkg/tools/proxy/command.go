package proxy

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/proxyclient"
	"github.com/chainreactors/proxyclient/extra/clash"
)

type OnProxyChange func(newProxyURL string)

// CommandExecutor executes a named command with args. Matches CommandRegistry.ExecuteArgs.
type CommandExecutor func(ctx context.Context, tokens []string) (string, error)

type Command struct {
	state         *State
	onProxyChange OnProxyChange
	execCommand   CommandExecutor
}

func New(state *State) *Command {
	return &Command{state: state}
}

func (c *Command) SetOnProxyChange(fn OnProxyChange) {
	c.onProxyChange = fn
}

func (c *Command) SetCommandExecutor(fn CommandExecutor) {
	c.execCommand = fn
}

func (c *Command) Name() string { return "proxy" }

func (c *Command) Usage() string {
	return `proxy - Manage proxy nodes and proxy-chain execution

Usage:
  proxy <proxy-url> <command> [args...]   Run a command through the specified proxy (like proxychains)
  proxy auto <url> [options]              Auto mode: subscribe + adaptive load balancing (recommended)
  proxy subscribe <url>                   Fetch a Clash subscription and list available nodes
  proxy list                              List loaded proxy nodes
  proxy switch <name|index>               Switch the active proxy node (single node)
  proxy test [name|index]                 Test proxy node connectivity
  proxy current                           Show the current active proxy
  proxy clear                             Clear subscription and revert to original proxy

Proxy-chain examples:
  proxy socks5://127.0.0.1:1080 gogo -i 10.0.0.1 -p top2
  proxy trojan://pass@host:443 zombie -i 10.0.0.1 -s ssh
  proxy 6 gogo -i 10.0.0.1 -p top2           Use subscribed node #6
  proxy HK gogo -i 10.0.0.1                   Use first node matching "HK"

Auto mode options:
  --type,-t  trojan,vless     Filter by protocol type
  --name,-n  keyword          Filter by node name keyword
  --country,-c HK,JP,US      Filter by server IP country (ISO 3166-1 alpha-2)
  --strategy,-s adaptive      Load balance strategy (adaptive, url-test, round-robin, random)`
}

func (c *Command) Execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(commands.Output, c.Usage())
		return nil
	}
	subcmd := strings.ToLower(args[0])
	rest := args[1:]

	var result string
	var err error

	if strings.Contains(args[0], "://") {
		result, err = c.execPassthrough(ctx, args[0], rest)
	} else {
		switch subcmd {
		case "auto":
			result, err = c.execAuto(ctx, rest)
		case "subscribe", "sub":
			result, err = c.execSubscribe(ctx, rest)
		case "list", "ls":
			result, err = c.execList()
		case "switch", "sw":
			result, err = c.execSwitch(rest)
		case "test":
			result, err = c.execTest(ctx, rest)
		case "current":
			result, err = c.execCurrent()
		case "clear":
			result, err = c.execClear()
		default:
			if len(rest) > 0 {
				if node, _, findErr := c.findNode(args[0]); findErr == nil {
					result, err = c.execPassthrough(ctx, node.URL.String(), rest)
				} else {
					return fmt.Errorf("unknown proxy subcommand: %s\n%s", subcmd, c.Usage())
				}
			} else {
				return fmt.Errorf("unknown proxy subcommand: %s\n%s", subcmd, c.Usage())
			}
		}
	}

	if err != nil {
		return err
	}
	if result != "" {
		fmt.Fprint(commands.Output, result)
	}
	return nil
}

// execPassthrough runs a command with a one-shot proxy override.
// The proxy is applied before the command runs and reverted after.
func (c *Command) execPassthrough(ctx context.Context, proxyURL string, cmdArgs []string) (string, error) {
	if len(cmdArgs) == 0 {
		return "", fmt.Errorf("usage: proxy <proxy-url> <command> [args...]\nexample: proxy socks5://127.0.0.1:1080 gogo -i 10.0.0.1 -p top2")
	}
	if c.execCommand == nil {
		return "", fmt.Errorf("proxy passthrough not available (no command executor)")
	}

	// validate proxy URL
	if _, err := url.Parse(proxyURL); err != nil {
		return "", fmt.Errorf("invalid proxy URL: %w", err)
	}

	// save current proxy, apply the one-shot proxy, restore after
	prev := c.state.ActiveProxy()
	if c.onProxyChange != nil {
		c.onProxyChange(proxyURL)
	}
	defer func() {
		if c.onProxyChange != nil {
			c.onProxyChange(prev)
		}
	}()

	return c.execCommand(ctx, cmdArgs)
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
	sb.WriteString("[proxy] auto mode enabled\n")
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
	if len(nodes) == 0 {
		return nil, 0, fmt.Errorf("no subscription loaded")
	}
	// try as index
	var idx int
	if _, err := fmt.Sscanf(nameOrIndex, "%d", &idx); err == nil {
		if idx < 1 || idx > len(nodes) {
			return nil, 0, fmt.Errorf("index %d out of range (1-%d)", idx, len(nodes))
		}
		return &nodes[idx-1], idx - 1, nil
	}
	lower := strings.ToLower(nameOrIndex)
	// exact name match
	for i := range nodes {
		if strings.ToLower(nodes[i].Name) == lower {
			return &nodes[i], i, nil
		}
	}
	// substring match (first supported node containing the keyword)
	for i := range nodes {
		if nodes[i].Supported && strings.Contains(strings.ToLower(nodes[i].Name), lower) {
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
