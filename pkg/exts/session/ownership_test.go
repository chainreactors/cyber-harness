package session_test

import (
 "context"
 "reflect"
 "testing"
 "github.com/chainreactors/cyber/agent"
 "github.com/chainreactors/cyber/core/extension"
 loopext "github.com/chainreactors/cyber/pkg/exts/agent"
 sessionext "github.com/chainreactors/cyber/pkg/exts/session"
)
type loopFunc func(context.Context,agent.Config)(*agent.Result,error)
func (f loopFunc) Run(ctx context.Context,c agent.Config)(*agent.Result,error){return f(ctx,c)}
func TestSessionCloseDoesNotCloseBorrowedLoop(t *testing.T){
 l,_:=loopext.New(loopext.Config{Loop:loopFunc(func(context.Context,agent.Config)(*agent.Result,error){return &agent.Result{Output:"alive"},nil})})
 s,err:=sessionext.New(sessionext.Config{Loop:l.Runtime()});if err!=nil{t.Fatal(err)}
 set,err:=extension.New(extension.Entry{ID:"loop",Extension:l},extension.Entry{ID:"session",DependsOn:[]string{"loop"},Extension:s});if err!=nil{t.Fatal(err)}
 defer set.Close(context.Background())
 if err:=set.Load(t.Context());err!=nil{t.Fatal(err)}
 if _,err:=s.Runtime().OpenSession(t.Context(),sessionext.SessionOptions{ID:"one"});err!=nil{t.Fatal(err)}
 if err:=s.Close(t.Context());err!=nil{t.Fatal(err)}
 if result,err:=l.Runtime().Run(t.Context(),agent.Config{});err!=nil||result.Output!="alive"{t.Fatalf("borrowed loop: %v %v",result,err)}
 for _,name:=range []string{"Start","Close","Load"}{
  if _,ok:=reflect.TypeFor[*sessionext.Runtime]().MethodByName(name);ok{t.Fatalf("runtime exposes %s",name)}
 }
}
