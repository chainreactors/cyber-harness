package node

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/runner"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/aiscan/skills"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ToolNodeConfig configures a tool-only runner: an outbound WebSocket
// connection exposing Command, native file RPCs, PTY, progress and raw tool
// artifacts,
// with no LLM provider, agent loop, or IOA dependency.
type ToolNodeConfig struct {
	ServerURL string
	WSPath    string
	// ID is the stable node identity used by Cairn as the runner primary key.
	ID        string
	Token     string
	Registry  *commands.CommandRegistry
	DataBus   *eventbus.Bus[output.ToolDataEvent]
	Artifacts *output.ArtifactStream
	Logger    telemetry.Logger
	Version   string
	// JSONFrames switches the hub wire to standard ProtoJSON text frames;
	// hubs expecting binary protobuf (AIScan) leave it false.
	JSONFrames bool
	// DisableCommandCatalog prevents the AIScan-specific command namespace from
	// being sent to generic AOP hubs. Tool definitions remain in AgentHello.
	DisableCommandCatalog bool
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
	home, _ := os.UserHomeDir()
	runnerRuntime.Metadata, _ = structpb.NewStruct(map[string]any{
		"version": cfg.Version,
		"mode":    "tool",
		"home":    home,
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
		"cores":   runtime.NumCPU(),
	})
	var menu func() []*types.CommandSpec
	if !cfg.DisableCommandCatalog {
		skillStore, _ := skills.LoadEmbeddedStore()
		menu = func() []*types.CommandSpec { return runner.RegistryCommandCatalog(cfg.Registry, skillStore) }
	}
	artifacts := cfg.Artifacts
	if artifacts == nil && cfg.DataBus != nil {
		artifacts = output.NewArtifactStream(cfg.DataBus)
		defer artifacts.Close()
	}
	return connect(ctx, connectionConfig{
		ServerURL:     cfg.ServerURL,
		WSPath:        cfg.WSPath,
		Name:          runnerID,
		Token:         cfg.Token,
		Registry:      cfg.Registry,
		DataBus:       cfg.DataBus,
		Artifacts:     artifacts,
		Logger:        logger,
		NodeID:        runnerID,
		Runtime:       runnerRuntime,
		Capabilities:  []string{"pty", "file", "exec", "tool", "artifact"},
		Menu:          menu,
		RunnerFileRPC: true,
		JSONFrames:    cfg.JSONFrames,
	})
}

// attachToolEvents forwards progress and scanner-native artifacts onto the hub
// connection. Nodes never normalize artifacts into SCO.
func attachToolEvents(dataBus *eventbus.Bus[output.ToolDataEvent], artifacts *output.ArtifactStream, send func(string, protobuf.Message)) func() {
	if dataBus == nil && artifacts == nil {
		return nil
	}
	var unsub func()
	if dataBus != nil {
		unsub = dataBus.Subscribe(func(event output.ToolDataEvent) {
			if event.Kind != output.ToolDataProgress {
				return
			}
			text, ok := event.Data.(string)
			if !ok || text == "" {
				return
			}
			timestamp := event.Timestamp
			if timestamp.IsZero() {
				timestamp = time.Now()
			}
			send(event.CallID, &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Progress{Progress: &toolpb.Progress{
				Tool: event.Tool, Target: event.Target, Text: text, Timestamp: timestamppb.New(timestamp),
			}}})
		})
	}
	if artifacts != nil {
		artifacts.SetHandler(func(artifact output.ToolArtifact) {
			timestamp := artifact.Timestamp
			if timestamp.IsZero() {
				timestamp = time.Now()
			}
			send(artifact.CallID, &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Artifact{Artifact: &toolpb.Artifact{
				Tool: artifact.Tool, Kind: artifact.Kind, Target: artifact.Target,
				Data: append([]byte(nil), artifact.Data...), MediaType: aop.JSONMediaType,
				Timestamp: timestamppb.New(timestamp),
			}}})
		})
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
		if artifacts != nil {
			artifacts.SetHandler(nil)
		}
	}
}
