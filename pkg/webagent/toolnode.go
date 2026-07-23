package webagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/ioa/protocols"
)

// ToolNodeConfig configures a tool-only node: an outbound WebSocket connection
// exposing AOP tool calls / native file RPCs / pty plus tool.data / tool.sco
// events, with no LLM provider, agent loop, or IOA dependency.
type ToolNodeConfig struct {
	ServerURL string
	WSPath    string
	// ID is the stable node identity used by Cairn as the runner primary key.
	ID string
	// Name is kept as a compatibility alias for callers created before ID was
	// made explicit. New callers should set ID.
	Name     string
	Token    string
	Registry *commands.CommandRegistry
	DataBus  *eventbus.Bus[output.ToolDataEvent]
	SCO      *output.SCOSidecar
	Logger   telemetry.Logger
	Version  string
}

// RunToolNode connects to the hub as a tool-only node and serves until ctx is
// done, reconnecting with backoff on connection loss.
func RunToolNode(ctx context.Context, cfg ToolNodeConfig) error {
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return fmt.Errorf("server URL is required")
	}
	if cfg.Registry == nil {
		return fmt.Errorf("command registry is required")
	}
	runnerID := strings.TrimSpace(cfg.ID)
	if runnerID == "" {
		runnerID = strings.TrimSpace(cfg.Name)
	}
	if runnerID == "" {
		runnerID, _ = os.Hostname()
	}
	baseURL, _ := SplitAccessKey(cfg.ServerURL)
	authority, err := protocols.CanonicalAuthority(baseURL)
	if err != nil {
		return fmt.Errorf("tool node authority: %w", err)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	runtime := DefaultRuntime()
	runtime.Capabilities = append(runtime.Capabilities, "file.read", "file.write", "file.list", "file.mkdir")
	runtime.Meta = map[string]any{"version": cfg.Version, "mode": "tool"}
	return connect(ctx, connectionConfig{
		ServerURL:     cfg.ServerURL,
		WSPath:        cfg.WSPath,
		Name:          runnerID,
		Token:         cfg.Token,
		Registry:      cfg.Registry,
		DataBus:       cfg.DataBus,
		SCO:           cfg.SCO,
		Logger:        logger,
		Node:          protocols.NodeRef{ID: runnerID, Authority: authority},
		Runtime:       runtime,
		RunnerFileRPC: true,
	})
}

// attachToolEvents forwards scanner telemetry (tool.data) and normalized SCO
// nodes (tool.sco) onto the hub connection, correlated by call ID. Returns an
// idempotent detach func, or nil when both sources are absent.
func attachToolEvents(dataBus *eventbus.Bus[output.ToolDataEvent], sco *output.SCOSidecar, send func(webproto.Message)) func() {
	if dataBus == nil && sco == nil {
		return nil
	}
	var unsub func()
	if dataBus != nil {
		unsub = dataBus.Subscribe(func(event output.ToolDataEvent) {
			send(webproto.Message{Type: "tool.data", TaskID: event.CallID, Payload: webproto.MustJSON(event)})
		})
	}
	if sco != nil {
		sco.OnNodes = func(callID string, nodes []json.RawMessage) {
			send(webproto.Message{Type: "tool.sco", TaskID: callID, Payload: webproto.MustJSON(map[string]any{"nodes": nodes})})
		}
	}
	var once bool
	return func() {
		if once {
			return
		}
		once = true
		if unsub != nil {
			unsub()
		}
		if sco != nil {
			sco.OnNodes = nil
		}
	}
}
