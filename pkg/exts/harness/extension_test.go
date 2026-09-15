package harness_test

import (
 "context"
 "errors"
 "reflect"
 "testing"
 "time"
 "github.com/chainreactors/cyber/core/extension"
 "github.com/chainreactors/cyber/core/hooks"
 "github.com/chainreactors/cyber/core/tool"
 "github.com/chainreactors/cyber/pkg/commands"
 harnessext "github.com/chainreactors/cyber/pkg/exts/harness"
)

type blockingTool struct { entered,release chan struct{} }
func (*blockingTool) Name() string { return "block" }
func (*blockingTool) Description() string { return "test" }
func (b *blockingTool) Definition() *tool.Definition { return tool.Def(b.Name(),b.Description(),struct{}{}) }
func (b *blockingTool) Execute(ctx context.Context,_ string)(*tool.Result,error) {
 close(b.entered); <-ctx.Done(); <-b.release; return nil,ctx.Err()
}
func TestHarnessDrainsBeforeContributorAndDoesNotLendClose(t *testing.T) {
 h,err:=harnessext.New(hooks.New()); if err!=nil {t.Fatal(err)}
 if _,ok:=reflect.TypeFor[commands.Executor]().MethodByName("Close"); ok {t.Fatal("execution interface owns close")}
 b:=&blockingTool{entered:make(chan struct{}),release:make(chan struct{})}
 closed:=false
 contributor:=extension.Func{LoadFunc:func(*extension.Scope)error{return h.ToolRegistry().Register("test",b)},CloseFunc:func(context.Context)error{closed=true;return nil}}
 set,err:=extension.New(extension.Entry{ID:"tools",Extension:contributor},extension.Entry{ID:"harness",DependsOn:[]string{"tools"},Extension:h})
 if err!=nil {t.Fatal(err)}
 if _,err:=h.Tools().ExecuteTool(t.Context(),"block","{}"); err==nil {t.Fatal("published before load")}
 if err:=set.Load(t.Context());err!=nil{t.Fatal(err)}
 done:=make(chan struct{})
 go func(){defer close(done); _,_=h.Tools().ExecuteTool(context.Background(),"block","{}")}()
 <-b.entered
 ctx,cancel:=context.WithTimeout(t.Context(),20*time.Millisecond);defer cancel()
 if err:=set.Close(ctx);!errors.Is(err,extension.ErrCloseIncomplete){t.Fatalf("close: %v",err)}
 if closed{t.Fatal("released contributor before drain")}
 close(b.release);<-done
 if err:=set.Close(t.Context());err!=nil{t.Fatal(err)}
 if !closed{t.Fatal("contributor not released")}
}
