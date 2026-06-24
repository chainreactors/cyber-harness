package proxy

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/proxyclient"
	"github.com/chainreactors/proxyclient/extra/clash"
	goflags "github.com/jessevdk/go-flags"
)

type OnProxyChange func(newProxyURL string)

type CommandExecutor func(ctx context.Context, tokens []string) (string, error)

type Command struct {
	state         *State
	mitmState     *MITMState
	onProxyChange OnProxyChange
	execCommand   CommandExecutor
}

func New(state *State) *Command {
	return &Command{
		state:     state,
		mitmState: NewMITMState(),
	}
}

func (c *Command) SetOnProxyChange(fn OnProxyChange) { c.onProxyChange = fn }
func (c *Command) SetCommandExecutor(fn CommandExecutor) { c.execCommand = fn }
func (c *Command) Name() string                         { return "proxy" }

func (c *Command) Usage() string {
	return `proxy - Manage proxy nodes, proxy-chain execution, and MITM traffic capture

Usage:
  proxy <proxy-url> <command> [args...]   Run a command through the specified proxy (like proxychains)
  proxy auto <url> [options]              Auto mode: subscribe + adaptive load balancing (recommended)
  proxy subscribe <url>                   Fetch a Clash subscription and list available nodes
  proxy list                              List loaded proxy nodes
  proxy switch <name|index>               Switch the active proxy node (single node)
  proxy test [name|index]                 Test proxy node connectivity
  proxy current                           Show the current active proxy
  proxy clear                             Clear subscription and revert to original proxy

MITM (man-in-the-middle) traffic capture:
  proxy mitm start [--addr HOST:PORT]     Start MITM proxy, route engine traffic through it
  proxy mitm stop                         Stop MITM and restore previous proxy
  proxy mitm status                       Show MITM status and flow count
  proxy mitm flows [--host X] [--last N]  List captured flows
  proxy mitm flow <id>                    Show full flow details
  proxy mitm clear                        Clear captured flows
  proxy mitm analyze [--host X] [--last N] Format flows for AI security analysis

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
		case "mitm":
			result, err = c.execMITM(ctx, rest)
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

// parseFlags is a helper that wraps goflags.ParseArgs and returns remaining positional args.
func parseFlags(f interface{}, args []string) ([]string, error) {
	p := goflags.NewParser(f, goflags.Default&^goflags.PrintErrors&^goflags.HelpFlag)
	remaining, err := p.ParseArgs(args)
	if err != nil {
		return nil, err
	}
	return remaining, nil
}

// ---------------------------------------------------------------------------
// passthrough
// ---------------------------------------------------------------------------

func (c *Command) execPassthrough(ctx context.Context, proxyURL string, cmdArgs []string) (string, error) {
	if len(cmdArgs) == 0 {
		return "", fmt.Errorf("usage: proxy <proxy-url> <command> [args...]\nexample: proxy socks5://127.0.0.1:1080 gogo -i 10.0.0.1 -p top2")
	}
	if c.execCommand == nil {
		return "", fmt.Errorf("proxy passthrough not available (no command executor)")
	}
	if _, err := url.Parse(proxyURL); err != nil {
		return "", fmt.Errorf("invalid proxy URL: %w", err)
	}

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

// ---------------------------------------------------------------------------
// auto
// ---------------------------------------------------------------------------

type autoFlags struct {
	Type     string `short:"t" long:"type" description:"Filter by protocol type (trojan,vless)"`
	Name     string `short:"n" long:"name" description:"Filter by node name keyword"`
	Country  string `short:"c" long:"country" description:"Filter by server IP country (HK,JP,US)"`
	Strategy string `short:"s" long:"strategy" description:"Load balance strategy" default:"adaptive"`
}

func (c *Command) execAuto(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: proxy auto <subscription-url> [--type trojan,vless] [--name keyword] [--country HK,JP] [--strategy adaptive]")
	}

	var f autoFlags
	remaining, err := parseFlags(&f, args)
	if err != nil {
		return "", fmt.Errorf("proxy auto: %w", err)
	}
	if len(remaining) == 0 {
		return "", fmt.Errorf("usage: proxy auto <subscription-url> [options]")
	}
	subURL := remaining[0]

	q := url.Values{}
	q.Set("url", subURL)
	q.Set("strategy", f.Strategy)
	q.Set("ua", "clash-verge/v2.0.0")
	if f.Type != "" {
		q.Set("type", f.Type)
	}
	if f.Name != "" {
		q.Set("name", f.Name)
	}
	if f.Country != "" {
		q.Set("country", f.Country)
	}
	clashURL := "clash://?" + q.Encode()

	sub, err := clash.FetchSubscriptionWithUA(subURL, "clash-verge/v2.0.0")
	if err != nil {
		return "", fmt.Errorf("fetch subscription: %w", err)
	}
	c.state.LoadSubscription(sub, subURL)

	u, _ := url.Parse(clashURL)
	dial, err := proxyclient.NewClient(u)
	if err != nil {
		return "", fmt.Errorf("create dialer: %w", err)
	}
	c.state.SetAutoDial(clashURL, dial)

	if c.onProxyChange != nil {
		c.onProxyChange(clashURL)
	}

	supported := clash.SupportedNodes(sub)
	var sb strings.Builder
	sb.WriteString("[proxy] auto mode enabled\n")
	sb.WriteString(fmt.Sprintf("  Subscription: %s\n", subURL))
	sb.WriteString(fmt.Sprintf("  Nodes: %d total, %d supported\n", len(sub.Nodes), len(supported)))
	sb.WriteString(fmt.Sprintf("  Strategy: %s\n", f.Strategy))
	if f.Type != "" {
		sb.WriteString(fmt.Sprintf("  Type filter: %s\n", f.Type))
	}
	if f.Name != "" {
		sb.WriteString(fmt.Sprintf("  Name filter: %s\n", f.Name))
	}
	if f.Country != "" {
		sb.WriteString(fmt.Sprintf("  Country filter: %s\n", f.Country))
	}
	sb.WriteString("  All traffic will auto-route through healthy nodes.")
	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// subscribe / list / switch / test / current / clear
// ---------------------------------------------------------------------------

func (c *Command) execSubscribe(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: proxy subscribe <clash-subscription-url>")
	}
	sub, err := clash.FetchSubscriptionWithUA(args[0], "clash-verge/v2.0.0")
	if err != nil {
		return "", fmt.Errorf("fetch subscription: %w", err)
	}
	c.state.LoadSubscription(sub, args[0])
	return c.formatNodeList(sub.Nodes, ""), nil
}

func (c *Command) execList() (string, error) {
	nodes := c.state.Nodes()
	if len(nodes) == 0 {
		return "[proxy] no subscription loaded. Use: proxy subscribe <url>", nil
	}
	return c.formatNodeList(nodes, c.state.ActiveNodeName()), nil
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
	return fmt.Sprintf("[proxy] switched to %q\nProxy URL: %s", c.state.ActiveNodeName(), newProxy), nil
}

func (c *Command) execTest(ctx context.Context, args []string) (string, error) {
	nodes := c.state.Nodes()
	if len(nodes) == 0 {
		return "", fmt.Errorf("no subscription loaded")
	}
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
	if c.mitmState.IsRunning() {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("[proxy] MITM active on %s\n", c.mitmState.ListenAddr()))
		saved := c.mitmState.SavedProxy()
		if saved != "" {
			sb.WriteString(fmt.Sprintf("  Upstream: %s\n", saved))
		}
		sb.WriteString(fmt.Sprintf("  Flows: %d captured", c.mitmState.Store().Count()))
		return sb.String(), nil
	}
	if c.state.IsAutoMode() {
		return fmt.Sprintf("[proxy] auto mode (adaptive load balancing)\nClash URL: %s", c.state.ActiveProxy()), nil
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

// ---------------------------------------------------------------------------
// MITM subcommands
// ---------------------------------------------------------------------------

type mitmStartFlags struct {
	Addr string `long:"addr" description:"Listen address for MITM proxy" default:"127.0.0.1:0"`
}

type mitmQueryFlags struct {
	Host   string `long:"host" description:"Filter flows by host substring"`
	Status string `long:"status" description:"Filter flows by status code (e.g. 2xx, 404, 5xx)"`
	Type   string `long:"type" description:"Filter flows by Content-Type substring"`
	Last   int    `long:"last" description:"Show only the last N flows"`
}

type mitmAnalyzeFlags struct {
	Host string `long:"host" description:"Filter flows by host substring"`
	Last int    `long:"last" description:"Analyze only the last N flows"`
}

func (c *Command) execMITM(_ context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: proxy mitm <start|stop|status|flows|flow|clear|analyze> [options]")
	}

	sub := strings.ToLower(args[0])
	rest := args[1:]

	switch sub {
	case "start":
		return c.mitmStart(rest)
	case "stop":
		return c.mitmStop()
	case "status":
		return c.mitmStatus()
	case "flows":
		return c.mitmFlows(rest)
	case "flow":
		return c.mitmFlowDetail(rest)
	case "clear":
		c.mitmState.Store().Clear()
		return "[mitm] flow store cleared", nil
	case "analyze":
		return c.mitmAnalyze(rest)
	default:
		return "", fmt.Errorf("unknown mitm subcommand: %s", sub)
	}
}

func (c *Command) mitmStart(args []string) (string, error) {
	if c.mitmState.IsRunning() {
		return "", fmt.Errorf("[mitm] already running on %s. Use 'proxy mitm stop' first", c.mitmState.ListenAddr())
	}

	var f mitmStartFlags
	if _, err := parseFlags(&f, args); err != nil {
		return "", fmt.Errorf("proxy mitm start: %w", err)
	}

	currentProxy := c.state.ActiveProxy()
	c.mitmState.SetSavedProxy(currentProxy)

	if err := c.mitmState.Start(f.Addr, currentProxy); err != nil {
		return "", err
	}

	if c.onProxyChange != nil {
		c.onProxyChange(c.mitmState.ProxyURL())
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[mitm] started on %s\n", c.mitmState.ListenAddr()))
	sb.WriteString("  All engine traffic now routed through MITM proxy\n")
	if currentProxy != "" {
		sb.WriteString(fmt.Sprintf("  Upstream: %s\n", currentProxy))
	}
	sb.WriteString("  Use 'proxy mitm flows' to view captured traffic")
	return sb.String(), nil
}

func (c *Command) mitmStop() (string, error) {
	if !c.mitmState.IsRunning() {
		return "[mitm] not running", nil
	}

	savedProxy := c.mitmState.SavedProxy()
	if err := c.mitmState.Stop(); err != nil {
		return "", fmt.Errorf("[mitm] stop error: %w", err)
	}

	if c.onProxyChange != nil {
		c.onProxyChange(savedProxy)
	}

	flowCount := c.mitmState.Store().Count()
	msg := fmt.Sprintf("[mitm] stopped. %d flows captured", flowCount)
	if savedProxy != "" {
		msg += fmt.Sprintf("\n  Restored proxy: %s", savedProxy)
	}
	return msg, nil
}

func (c *Command) mitmStatus() (string, error) {
	if !c.mitmState.IsRunning() {
		flowCount := c.mitmState.Store().Count()
		if flowCount > 0 {
			return fmt.Sprintf("[mitm] stopped (%d flows in store)", flowCount), nil
		}
		return "[mitm] not running", nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[mitm] running on %s\n", c.mitmState.ListenAddr()))
	sb.WriteString(fmt.Sprintf("  Proxy URL: %s\n", c.mitmState.ProxyURL()))
	sb.WriteString(fmt.Sprintf("  Flows captured: %d\n", c.mitmState.Store().Count()))
	if saved := c.mitmState.SavedProxy(); saved != "" {
		sb.WriteString(fmt.Sprintf("  Upstream: %s\n", saved))
	}
	return sb.String(), nil
}

func (c *Command) mitmFlows(args []string) (string, error) {
	var f mitmQueryFlags
	if _, err := parseFlags(&f, args); err != nil {
		return "", fmt.Errorf("proxy mitm flows: %w", err)
	}
	flows := c.mitmState.Store().Query(QueryOpts{
		Host:   f.Host,
		Status: f.Status,
		CType:  f.Type,
		Last:   f.Last,
	})
	return formatFlowList(flows), nil
}

func (c *Command) mitmFlowDetail(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("usage: proxy mitm flow <id>")
	}
	var id int
	if _, err := fmt.Sscanf(args[0], "%d", &id); err != nil {
		return "", fmt.Errorf("invalid flow ID: %s", args[0])
	}
	f := c.mitmState.Store().Get(id)
	if f == nil {
		return "", fmt.Errorf("flow #%d not found", id)
	}
	return formatFlowDetail(f), nil
}

func (c *Command) mitmAnalyze(args []string) (string, error) {
	var f mitmAnalyzeFlags
	if _, err := parseFlags(&f, args); err != nil {
		return "", fmt.Errorf("proxy mitm analyze: %w", err)
	}
	flows := c.mitmState.Store().Query(QueryOpts{
		Host: f.Host,
		Last: f.Last,
	})
	return formatFlowAnalysis(flows), nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (c *Command) findNode(nameOrIndex string) (*clash.ProxyNode, int, error) {
	nodes := c.state.Nodes()
	if len(nodes) == 0 {
		return nil, 0, fmt.Errorf("no subscription loaded")
	}
	var idx int
	if _, err := fmt.Sscanf(nameOrIndex, "%d", &idx); err == nil {
		if idx < 1 || idx > len(nodes) {
			return nil, 0, fmt.Errorf("index %d out of range (1-%d)", idx, len(nodes))
		}
		return &nodes[idx-1], idx - 1, nil
	}
	lower := strings.ToLower(nameOrIndex)
	for i := range nodes {
		if strings.ToLower(nodes[i].Name) == lower {
			return &nodes[i], i, nil
		}
	}
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
	for t, cnt := range typeCount {
		typeSummary = append(typeSummary, fmt.Sprintf("%s:%d", t, cnt))
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
