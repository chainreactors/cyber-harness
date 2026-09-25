package node

import (
	aop "github.com/chainreactors/cyber/aop"
	filepb "github.com/chainreactors/cyber/aop/file"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/eventbus"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
)

type connectionConfig struct {
	ServerURL string
	Name      string
	Executor  coretool.Executor
	// Events is the runtime's canonical event stream for this connection.
	Events       *coreevents.Stream
	Progress     *eventbus.Bus[*toolpb.Progress]
	Logger       telemetry.Logger
	Upload       func(*filepb.UploadRequest) (*filepb.Result, error)
	ReloadConfig func(*types.DistributeConfig) (*types.ReloadResult, *aop.AgentStatus)
	CommitReload func()
	NodeID       string
	Runtime      *aop.AgentRuntimeInfo
	Status       func() *aop.AgentStatus
	Menu         func() []*types.CommandSpec
	// RegisterNamespaces binds control protocols backed by resources
	// owned by the loaded profile. The connection owns only their registrations.
	RegisterNamespaces func(*aop.NamespaceMux) error
}
