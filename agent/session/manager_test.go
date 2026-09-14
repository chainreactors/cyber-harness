package session

import (
 "context"
 "fmt"
 "sync"
 "testing"
 "github.com/chainreactors/aiscan/agent"
 "github.com/chainreactors/aiscan/aop"
 "github.com/chainreactors/aiscan/pkg/types"
)

func TestStandaloneManagerAndLiveCommandRegistration(t *testing.T) {
 m, err := NewManager(Config{Loop: agent.StandardLoop{}})
 if err != nil { t.Fatal(err) }
 if err := m.Start(t.Context(), t.Context()); err != nil { t.Fatal(err) }
 t.Cleanup(func(){ if err := m.Close(context.Background()); err != nil { t.Error(err) } })
 first, err := m.OpenSession(t.Context(), SessionOptions{ID:"first"})
 if err != nil { t.Fatal(err) }
 second, err := m.OpenSession(t.Context(), SessionOptions{ID:"second"})
 if err != nil { t.Fatal(err) }
 spec := &types.CommandSpec{Name:"/identity", Aliases:[]string{"/id"}}
 err = m.RegisterCommand(Command{Spec:spec, AdvertiseRemote:true,
 Handler:func(_ context.Context, s *Session, _ []string)(*types.CommandResult,error) {
  // Registration is reentrant; the dispatch lock must not cover handlers.
  _ = m.CommandSpecs(true)
  return &types.CommandResult{Content:[]*aop.Content{aop.Text(s.ID())}}, nil
 }})
 if err != nil { t.Fatal(err) }
 spec.Name = "/mutated"
 for _, s := range []*Session{first,second} {
  result, err := s.Command(t.Context(), "/id")
  if err != nil || result.Content[0].GetText().Text != s.ID() { t.Fatalf("command: %v %v", result,err) }
 }
 before := len(m.CommandSpecs(false))
 if err := m.RegisterCommand(Command{Spec:&types.CommandSpec{Name:"/new",Aliases:[]string{"/id"}}, Handler:func(context.Context,*Session,[]string)(*types.CommandResult,error){return nil,nil}}); err == nil {
  t.Fatal("accepted conflicting alias")
 }
 if len(m.CommandSpecs(false)) != before { t.Fatal("partial command publication") }
 if _,ok := m.lookupCommand("/new"); ok { t.Fatal("conflicting batch leaked") }
 if err := m.Close(t.Context()); err != nil { t.Fatal(err) }
 if err := m.RegisterCommand(Command{Spec:&types.CommandSpec{Name:"/late"},Handler:func(context.Context,*Session,[]string)(*types.CommandResult,error){return nil,nil}}); err == nil { t.Fatal("registration after close") }
 if _,err := first.Command(t.Context(),"/id"); err == nil { t.Fatal("command after close") }
}

func TestConcurrentCommandRegistrationAndDiscovery(t *testing.T) {
 m,err := NewManager(Config{})
 if err != nil { t.Fatal(err) }
 if err := m.Start(t.Context(),t.Context()); err != nil { t.Fatal(err) }
 defer m.Close(context.Background())
 var wg sync.WaitGroup
 for i:=0;i<24;i++ {
  wg.Add(1)
  go func(i int){ defer wg.Done()
   name:=fmt.Sprintf("/custom%d",i)
   if err := m.RegisterCommand(Command{Spec:&types.CommandSpec{Name:name},Handler:func(context.Context,*Session,[]string)(*types.CommandResult,error){return nil,nil}}); err!=nil { t.Error(err) }
   _=m.CommandSpecs(false)
   if _,ok:=m.lookupCommand(name); !ok { t.Errorf("missing %s",name) }
  }(i)
 }
 wg.Wait()
}
