package session
import (
 "context"
 "github.com/chainreactors/cyber/agent"
 inboxpkg "github.com/chainreactors/cyber/agent/inbox"
 "github.com/chainreactors/cyber/aop"
 cfg "github.com/chainreactors/cyber/core/config"
 "github.com/chainreactors/cyber/core/eventbus"
 coreevents "github.com/chainreactors/cyber/core/events"
 "github.com/chainreactors/cyber/core/telemetry"
 "github.com/chainreactors/cyber/pkg/types"
 protobuf "google.golang.org/protobuf/proto"
)
// Operations is the borrowed session surface. Start and Close stay with the
// extension's resource, not with hosts receiving Runtime.
type Operations interface {
OpenSession(ctx context.Context, options SessionOptions) (*Session, error)
EnsureSession(options SessionOptions) (*Session, error)
CloseSession(ctx context.Context, sessionID string, reason SessionCloseReason) error
Observe(observer coreevents.Observer) *eventbus.Subscription[*aop.Event]
Publish(event *aop.Event)
RunSession(ctx context.Context, sessionID string, input RunInput) (*Run, error)
CommandSession(ctx context.Context, sessionID, line string) (*types.CommandResult, error)
CancelRun(turnID string) error
CancelSessionRun(sessionID, turnID string) error
WaitOperations()
Deliver(ctx context.Context, message inboxpkg.Message) error
NodeName() string
SetLogger(logger telemetry.Logger)
ReloadProvider(option *cfg.Option) (agent.Provider, string, error)
ReloadResolvedProvider(config agent.ProviderConfig) (agent.Provider, agent.ProviderConfig, error)
SetProvider(provider agent.Provider, providerConfig agent.ProviderConfig)
Context() context.Context
OpenAOPSession(req *aop.OpenSessionRequest) *aop.OpenSessionResponse
RunAOPTurn(ctx context.Context, req *aop.RunTurnRequest) *aop.RunTurnResponse
CancelAOPTurn(req *aop.CancelTurnRequest) *aop.CancelTurnResponse
CloseAOPSession(ctx context.Context, req *aop.CloseSessionRequest) *aop.CloseSessionResponse
RegisterNamespaces(mux *aop.NamespaceMux) error
HandleCoreNamespace(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, send aop.SendFunc) error
HandleCommandNamespace(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, send aop.SendFunc) error
RegisterCommand(command Command) error
CommandSpecs(remote bool) []*types.CommandSpec
}
