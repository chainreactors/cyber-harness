package node

import (
	"context"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	coreterminal "github.com/chainreactors/aiscan/core/terminal"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/runner"
	types "github.com/chainreactors/aiscan/pkg/types"
)

const DefaultWSPath = "/api/aop/node/ws"

type connectionConfig struct {
	ServerURL    string
	WSPath       string
	Name         string
	Token        string
	Capabilities []string

	// JSONFrames switches the wire codec from binary protobuf to standard
	// ProtoJSON text frames (used by hubs that speak JSON, e.g. Cairn).
	JSONFrames     bool
	Registry       *commands.CommandRegistry
	AgentSubscribe func(func(*aop.Event)) func()
	DataBus        *eventbus.Bus[output.ToolDataEvent]
	Artifacts      *output.ArtifactStream
	Logger         telemetry.Logger
	Chat           *chatAgentHandler
	// AgentRuntime handles the AOP core/command namespaces directly via
	// HandleEnvelope; nil on tool-only nodes, which reject chat messages.
	AgentRuntime  *runner.AgentRuntime
	NodeID        string
	Runtime       *aop.AgentRuntimeInfo
	Status        func() *aop.AgentStatus
	Menu          func() []*types.CommandSpec
	RunnerFileRPC bool
	PTYRouter     func() (*coreterminal.Router, error)
}

func connect(ctx context.Context, config connectionConfig) error {
	return connectGenerated(ctx, config)
}
