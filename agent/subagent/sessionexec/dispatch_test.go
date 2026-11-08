package sessionexec

import (
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/subagent"
	aop "github.com/chainreactors/cyber/aop"
)

func TestDispatchPreparedSeedPreservesEnvelopeAndContinuation(t *testing.T) {
	observed := make(chan []inbox.Message, 2)
	cfg := agent.Config{SessionID: "parent", Provider: &scriptedProvider{}, Loop: taskLoop(func(_ context.Context, cfg agent.Config) (*agent.Result, error) {
		inputs := cfg.Inbox.Drain()
		observed <- inputs
		messages := append([]*aop.Message(nil), cfg.Messages...)
		for _, m := range inputs {
			messages = append(messages, m.Message)
		}
		return &agent.Result{Messages: messages, Output: "done"}, nil
	})}
	tool := newSubagentTestTool(t, cfg)
	_, err := tool.registry.Add(subagent.Subagent{Name: "prepared", Prepare: func(_ context.Context, cfg agent.Config, input subagent.Input) (agent.Config, string, error) {
		return cfg, "prepared: " + input.Prompt, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	seed := inbox.NewMessage(inbox.OriginPeer, "user", "raw seed")
	seed.Message.Id, seed.Message.Name = "seed-id", "requester"
	seed.Message.Content = append(seed.Message.Content, aop.MediaData("image", "image/png", "evidence.png", []byte("media-payload")))
	seed.Meta = inbox.Metadata{Sender: "peer", MessageID: "remote-id"}
	seed.Attachments = []inbox.Attachment{{Type: "raw", Content: "attachment data"}}
	seed.Priority, seed.Interrupt = inbox.PriorityHigh, true
	var snapshot []*aop.Message
	_, err = tool.Dispatch(t.Context(), subagent.Request{Name: "prepared", Input: subagent.Input{Prompt: "raw seed"}}, session.SessionOptions{ID: "first"}, []inbox.Message{seed}, func(m []*aop.Message, _ *agent.Result, _ error) error { snapshot = m; return nil })
	if err != nil {
		t.Fatal(err)
	}
	got := <-observed
	if len(got) != 1 {
		t.Fatalf("seed count=%d", len(got))
	}
	m := got[0]
	if provider.MessageText(m.Message) != "prepared: raw seed" || m.Message.Id != seed.Message.Id || m.Message.Name != seed.Message.Name || m.Origin != seed.Origin || m.Priority != seed.Priority || m.Interrupt != seed.Interrupt || m.Meta.MessageID != "remote-id" || len(m.Attachments) != 1 || !m.CreatedAt.Equal(seed.CreatedAt) {
		t.Fatalf("Prepare changed the source envelope: %+v", m)
	}
	if provider.MessageText(seed.Message) != "raw seed" {
		t.Fatal("Prepare mutated caller's seed")
	}
	if len(m.Message.Content) != 2 || string(m.Message.Content[1].GetMedia().GetResource().GetData()) != "media-payload" {
		t.Fatal("Prepare discarded non-text content")
	}
	correction := inbox.NewUserMessage("correction")
	correction.Message.Id = "correction-id"
	_, err = tool.Dispatch(t.Context(), subagent.Request{Name: "prepared", Input: subagent.Input{Prompt: "correction"}}, session.SessionOptions{ID: "second", Messages: snapshot}, []inbox.Message{seed, correction}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got = <-observed
	if len(got) != 1 || got[0].Message.Id != "correction-id" || strings.Contains(provider.MessageText(got[0].Message), "prepared:") {
		t.Fatalf("continuation duplicated or transformed input: %+v", got)
	}
}

func TestDispatchWithoutEnvelopesUsesPreparedInput(t *testing.T) {
	observed := make(chan string, 1)
	tool := newSubagentTestTool(t, agent.Config{SessionID: "parent", Provider: &scriptedProvider{}, Loop: taskLoop(func(_ context.Context, cfg agent.Config) (*agent.Result, error) {
		inputs := cfg.Inbox.Drain()
		if len(inputs) == 1 {
			observed <- provider.MessageText(inputs[0].Message)
		} else {
			observed <- "missing"
		}
		return &agent.Result{Output: "done"}, nil
	})})
	_, err := tool.registry.Add(subagent.Subagent{Name: "prepared", Prepare: func(_ context.Context, cfg agent.Config, input subagent.Input) (agent.Config, string, error) {
		return cfg, "prepared: " + input.Prompt, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Dispatch(t.Context(), subagent.Request{Name: "prepared", Input: subagent.Input{Prompt: "seed"}}, session.SessionOptions{ID: "dispatch"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-observed; got != "prepared: seed" {
		t.Fatalf("nil-envelope dispatch discarded task: %q", got)
	}
}
