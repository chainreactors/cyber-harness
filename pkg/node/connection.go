package node

import (
	"context"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/eventbus"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/commands"
	agentext "github.com/chainreactors/cyber/pkg/exts/session"
	"github.com/chainreactors/cyber/pkg/terminal"
	types "github.com/chainreactors/cyber/pkg/types"
)

// agentEndpoint is the sole event ingress/egress point for a node connection.
// Keeping publication and subscription on one object prevents a terminal event
// from being sent both through the runtime bus and as a direct protocol reply.
type agentEndpoint interface {
	Observe(coreevents.Observer) *eventbus.Subscription[*aop.Event]
	Publish(*aop.Event)
}

type connectionConfig struct {
	ServerURL         string
	WSPath            string
	Name              string
	Token             string
	Capabilities      []string
	ExtraCapabilities []string

	// JSONFrames switches the wire codec from binary protobuf to standard
	// ProtoJSON text frames (used by hubs that speak JSON, e.g. Cairn).
	JSONFrames bool
	Executor   tool.Executor
	// Registry supplies the Bash pseudo-command projection to Cyber agent nodes.
	Registry commands.Executor
	Bash     *commands.BashTool
	// Agent owns connection-side events. Control uses the product runtime;
	// nil denotes a tool-only node. No optional interface selects routing.
	Control       *agentext.Runtime
	Agent         agentEndpoint
	Progress      *eventbus.Bus[*toolpb.Progress]
	Logger        telemetry.Logger
	Chat          *chatAgentHandler
	NodeID        string
	Runtime       *aop.AgentRuntimeInfo
	Status        func() *aop.AgentStatus
	Menu          func() []*types.CommandSpec
	RunnerFileRPC bool
	Hooks         *hooks.Registry
	PTYRouter     func() (*terminal.Router, error)
	// RegisterResourceNamespaces binds control protocols backed by resources
	// owned by the loaded profile. The connection owns only their registrations.
	RegisterResourceNamespaces func(*aop.NamespaceMux) error
}

func connect(ctx context.Context, config connectionConfig) error {
	return connectGenerated(ctx, config)
}
