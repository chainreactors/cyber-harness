package node

import (
	"context"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/terminal"
	types "github.com/chainreactors/aiscan/pkg/types"
)

const DefaultWSPath = "/api/aop/node/ws"

// agentEndpoint is the sole event ingress/egress point for a node connection.
// Agent runtimes implement the optional control method as well; tool-only
// nodes use the eventBusEndpoint adapter below. Keeping publication and
// subscription on one object prevents a terminal event from being sent both
// through the runtime bus and as a direct protocol reply.
type agentEndpoint interface {
	Subscribe(func(*aop.Event)) func()
	EmitEvent(*aop.Event)
}

type agentControlEndpoint interface {
	agentEndpoint
	HandleEnvelope(context.Context, *aop.Envelope, func(*aop.Envelope)) bool
}

type eventBusEndpoint struct {
	bus *eventbus.Bus[*aop.Event]
}

func newEventBusEndpoint(bus *eventbus.Bus[*aop.Event]) agentEndpoint {
	if bus == nil {
		bus = eventbus.New[*aop.Event]()
	}
	return &eventBusEndpoint{bus: bus}
}

func (e *eventBusEndpoint) Subscribe(fn func(*aop.Event)) func() {
	if e == nil || e.bus == nil {
		return func() {}
	}
	return e.bus.Subscribe(fn)
}

func (e *eventBusEndpoint) EmitEvent(event *aop.Event) {
	if e == nil || e.bus == nil || event == nil {
		return
	}
	e.bus.Emit(event)
}

type connectionConfig struct {
	ServerURL    string
	WSPath       string
	Name         string
	Token        string
	Capabilities []string

	// JSONFrames switches the wire codec from binary protobuf to standard
	// ProtoJSON text frames (used by hubs that speak JSON, e.g. Cairn).
	JSONFrames bool
	Registry   *commands.CommandRegistry
	// Agent is the single owner of connection-side events. Implementations that
	// also satisfy agentControlEndpoint handle core/command namespaces.
	Agent         agentEndpoint
	Progress      *eventbus.Bus[*toolpb.Progress]
	Logger        telemetry.Logger
	Chat          *chatAgentHandler
	NodeID        string
	Runtime       *aop.AgentRuntimeInfo
	Status        func() *aop.AgentStatus
	Menu          func() []*types.CommandSpec
	RunnerFileRPC bool
	// FileAudit is the node's file-access trail, streamed on the file namespace
	// and steerable by the peer through Configure.
	FileAudit *commands.FileAudit
	PTYRouter func() (*terminal.Router, error)
	// ExtraNamespaces registers additional AOP namespaces on the connection mux
	// after the built-ins (see ToolNodeConfig.ExtraNamespaces).
	ExtraNamespaces []func(*aop.NamespaceMux) error
}

func connect(ctx context.Context, config connectionConfig) error {
	return connectGenerated(ctx, config)
}
