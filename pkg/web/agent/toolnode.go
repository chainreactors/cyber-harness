package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/ioa/protocols"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ToolNodeConfig configures a tool-only runner: an outbound WebSocket
// connection exposing Command, native file RPCs, PTY, tool.data and tool.sco,
// with no LLM provider, agent loop, or IOA dependency.
type ToolNodeConfig struct {
	ServerURL string
	WSPath    string
	// ID is the stable node identity used by Cairn as the runner primary key.
	ID       string
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
	runnerRuntime := DefaultRuntime()
	runnerRuntime.Capabilities = append(runnerRuntime.Capabilities, "file.read", "file.write", "file.list", "file.mkdir")
	home, _ := os.UserHomeDir()
	runnerRuntime.Metadata, _ = aop.JSONValue(map[string]any{
		"version": cfg.Version,
		"mode":    "tool",
		"home":    home,
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
		"cores":   runtime.NumCPU(),
	})
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
		Runtime:       runnerRuntime,
		RunnerFileRPC: true,
	})
}

// attachToolEvents forwards scanner telemetry (tool.data) and normalized SCO
// nodes (tool.sco) onto the hub connection, correlated by call ID. Returns an
// idempotent detach func, or nil when both sources are absent.
func attachToolEvents(dataBus *eventbus.Bus[output.ToolDataEvent], sco *output.SCOSidecar, send func(*transport.AgentFrame)) func() {
	if dataBus == nil && sco == nil {
		return nil
	}
	var unsub func()
	if dataBus != nil {
		unsub = dataBus.Subscribe(func(event output.ToolDataEvent) {
			data, _ := aop.JSONValue(event.Data)
			timestamp := event.Timestamp
			if timestamp.IsZero() {
				timestamp = time.Now()
			}
			send(&transport.AgentFrame{CorrelationId: event.CallID, Payload: &transport.AgentFrame_ToolTelemetry{ToolTelemetry: &transport.ToolTelemetry{
				Tool: event.Tool, Kind: event.Kind, Target: event.Target, Data: data, CallId: event.CallID, Timestamp: timestamppb.New(timestamp),
			}}})
		})
	}
	if sco != nil {
		sco.OnNodes = func(callID string, nodes []json.RawMessage) {
			encoded := make([][]byte, 0, len(nodes))
			for _, node := range nodes {
				encoded = append(encoded, append([]byte(nil), node...))
			}
			send(&transport.AgentFrame{CorrelationId: callID, Payload: &transport.AgentFrame_ScoNodes{ScoNodes: &transport.ScoNodes{CallId: callID, Nodes: encoded}}})
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
