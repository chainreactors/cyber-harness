package agent

import (
	"context"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	commandpb "github.com/chainreactors/aiscan/pkg/types/command"
	"github.com/chainreactors/ioa/protocols"
	"github.com/chainreactors/utils/pty"
)

const DefaultWSPath = "/api/aop/ws"

type connectionConfig struct {
	ServerURL    string
	WSPath       string
	Name         string
	Token        string
	Capabilities []string

	Registry       *commands.CommandRegistry
	AgentSubscribe func(func(*aop.Event)) func()
	DataBus        *eventbus.Bus[output.ToolDataEvent]
	SCO            *output.SCOSidecar
	Logger         telemetry.Logger
	Chat           *chatAgentHandler
	Node           protocols.NodeRef
	Runtime        *aop.AgentRuntimeInfo
	Status         func() *aop.AgentStatus
	Menu           func() []*commandpb.Spec
	RunnerFileRPC  bool
	PTYRouter      func() (*pty.Router, error)
}

func connect(ctx context.Context, config connectionConfig) error {
	return connectGenerated(ctx, config)
}
