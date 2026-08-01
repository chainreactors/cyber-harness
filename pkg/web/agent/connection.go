package agent

import (
	"context"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/ioa/protocols"
	"github.com/chainreactors/utils/pty"
)

const DefaultWSPath = "/api/agent/ws"

type connectionConfig struct {
	ServerURL string
	WSPath    string
	Name      string
	Token     string

	Registry       *commands.CommandRegistry
	AgentSubscribe func(func(*aop.Event)) func()
	DataBus        *eventbus.Bus[output.ToolDataEvent]
	SCO            *output.SCOSidecar
	Logger         telemetry.Logger
	Chat           chatHandler
	Node           protocols.NodeRef
	Runtime        *transport.AgentRuntimeInfo
	Status         func() *transport.AgentStatus
	Menu           func() []*transport.CommandSpec
	RunnerFileRPC  bool
	PTYRouter      func() (*pty.Router, error)
}

type chatHandler interface {
	OpenSession(context.Context, *aop.OpenSessionRequest) *aop.OpenSessionResponse
	RunTurn(context.Context, *aop.RunTurnRequest) *aop.RunTurnResponse
	CancelTurn(*aop.CancelTurnRequest) *aop.CancelTurnResponse
	CloseSession(context.Context, *aop.CloseSessionRequest) *aop.CloseSessionResponse
	Command(context.Context, *transport.CommandRequest) (*transport.CommandResult, error)
	Upload(*transport.FileUploadRequest) (*transport.FileResult, error)
	ReloadConfig(string) (*transport.ConfigReloadResult, *transport.AgentStatus)
}

func connect(ctx context.Context, config connectionConfig) error {
	return connectGenerated(ctx, config, false)
}
