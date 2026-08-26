package node

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"runtime"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/runner"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/aiscan/skills"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// ToolNodeConfig configures a tool-only runner: an outbound WebSocket
// connection exposing Command, native file RPCs, PTY, progress and raw tool
// artifacts,
// with no LLM provider, agent loop, or IOA dependency.
type ToolNodeConfig struct {
	ServerURL string
	WSPath    string
	// ID is the stable node identity used by Cairn as the runner primary key.
	ID       string
	Token    string
	Registry *commands.CommandRegistry
	Events   *eventbus.Bus[*aop.Event]
	Progress *eventbus.Bus[*toolpb.Progress]
	Logger   telemetry.Logger
	Version  string
	// JSONFrames switches the hub wire to standard ProtoJSON text frames;
	// hubs expecting binary protobuf (AIScan) leave it false.
	JSONFrames bool
	// DisableCommandCatalog prevents the AIScan-specific command namespace from
	// being sent to generic AOP hubs. Tool definitions remain in AgentHello.
	DisableCommandCatalog bool
	// FileAudit is the file-access trail the registry's tools report into. When
	// set, every observation is streamed to the hub on the file namespace.
	FileAudit *commands.FileAudit
	// ExtraNamespaces lets a host register additional AOP namespaces on the
	// connection mux (e.g. the traffic namespace backed by the host's proxy
	// hub). Each registrar is applied after the built-in namespaces.
	ExtraNamespaces []func(*aop.NamespaceMux) error
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
	logger := cfg.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	runnerRuntime := runner.DefaultRuntimeInfo()
	instanceID := rand.Text()
	home, _ := os.UserHomeDir()
	runnerRuntime.Metadata, _ = structpb.NewStruct(map[string]any{
		"version":     cfg.Version,
		"mode":        "tool",
		"home":        home,
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"cores":       runtime.NumCPU(),
		"instance_id": instanceID,
	})
	var menu func() []*types.CommandSpec
	if !cfg.DisableCommandCatalog {
		skillStore, _ := skills.LoadEmbeddedStore()
		menu = func() []*types.CommandSpec { return runner.RegistryCommandCatalog(cfg.Registry, skillStore) }
	}
	return connect(ctx, connectionConfig{
		ServerURL:       cfg.ServerURL,
		WSPath:          cfg.WSPath,
		Name:            runnerID,
		Token:           cfg.Token,
		Registry:        cfg.Registry,
		Agent:           newEventBusEndpoint(cfg.Events),
		Progress:        cfg.Progress,
		Logger:          logger,
		NodeID:          runnerID,
		Runtime:         runnerRuntime,
		Capabilities:    []string{"pty", "file", "exec", "tool", "artifact"},
		Menu:            menu,
		RunnerFileRPC:   true,
		FileAudit:       cfg.FileAudit,
		JSONFrames:      cfg.JSONFrames,
		ExtraNamespaces: cfg.ExtraNamespaces,
	})
}

// attachToolProgress forwards ephemeral progress onto the tool protocol. Raw
// artifacts travel as canonical AOP extension events through the Agent
// endpoint's single event stream.
func attachToolProgress(progressBus *eventbus.Bus[*toolpb.Progress], send func(string, protobuf.Message)) func() {
	if progressBus == nil {
		return nil
	}
	unsub := progressBus.Subscribe(func(progress *toolpb.Progress) {
		if progress == nil || progress.Text == "" {
			return
		}
		send(progress.CallId, &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Progress{Progress: protobuf.CloneOf(progress)}})
	})
	var once bool
	return func() {
		if once {
			return
		}
		once = true
		if unsub != nil {
			unsub()
		}
	}
}
