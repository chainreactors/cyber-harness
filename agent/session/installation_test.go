package session
import (
 "context"
 "errors"
 "github.com/chainreactors/aiscan/agent"
 "github.com/chainreactors/aiscan/agent/prompt"
 "github.com/chainreactors/aiscan/core/extension"
 apppkg "github.com/chainreactors/aiscan/pkg/app"
 loopext "github.com/chainreactors/aiscan/pkg/exts/agent"
)
// Legacy integration scenarios exercise both resources through a test fixture.
// Production profiles install separate Agent and Session nodes.
type Extension struct { runtime *Runtime; loop *loopext.Extension; sessions bool }
func New(c Config) (*Extension,error) {
 sessions:=c.Application!=nil
 var loop *loopext.Extension
 if c.Loop!=nil || !sessions {
  var err error
  loop,err=loopext.New(loopext.Config{Loop:c.Loop});if err!=nil{return nil,err}
  c.Loop=loop.Runtime()
 }
 r,err:=NewManager(c);if err!=nil{return nil,err}
 return &Extension{runtime:r,loop:loop,sessions:sessions},nil
}
func(e *Extension) Runtime()*Runtime{return e.runtime}
func(e *Extension) Load(s *extension.Scope)error{
 if e.loop!=nil{if err:=e.loop.Load(s);err!=nil{return err}}
 if e.sessions{return e.runtime.Start(s.Init(),s.Lifetime())};return nil
}
func(e *Extension) Close(ctx context.Context)error{
 var err error
 if e.loop!=nil{err=e.loop.Close(ctx)}
 if e.sessions{err=errors.Join(err,e.runtime.Close(ctx))};return err
}
func(r *Runtime) Run(ctx context.Context,c agent.Config)(*agent.Result,error){
 if r.runtimeConfig.Loop==nil{return nil,ErrUnavailable}
 result,err:=r.runtimeConfig.Loop.Run(ctx,c)
 if errors.Is(err,loopext.ErrUnavailable){err=errors.Join(err,ErrUnavailable)}
 return result,err
}
var SystemPromptFunc=prompt.SystemPromptFunc
func testEnvironment(value any)*Environment{
 if e,ok:=value.(*Environment);ok{return e}
 a,ok:=value.(*apppkg.App);if !ok||a==nil{return nil}
 e:=&Environment{Tools:a.Tools,Commands:a.Commands,Skills:a.Skills,Hooks:a.Hooks,
 PublishEvent:a.Publish,ObserveEvents:a.ObserveEvents,ProviderState:a.ProviderState,
 SetProvider:a.SetProvider,ReloadProvider:a.ReloadProvider,ResolveProvider:apppkg.ProviderConfig,
 LLMHealth:a.LLMHealth,ScannerState:a.ScannerState,Logger:a.Logger,SetLogger:a.SetLogger}
 if a.Bash!=nil{e.Bash=a.Bash};return e
}
