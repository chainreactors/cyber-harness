package service

import (
	"bytes"
	"github.com/chainreactors/aiscan/aop"
	"testing"
)

func TestAgentStatusReplacesWholeSnapshot(t *testing.T) {
	pool := &AgentPool{}
	agent := &remoteAgent{nodeState: &nodeState{}, status: &aop.AgentStatus{Bound: true, Space: "old", ConfigError: "old error"}}
	status := &aop.AgentStatus{}
	status.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	pool.handleAgentCoreMessage(agent, &aop.Envelope{}, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentStatus{AgentStatus: status}})
	if agent.status.Bound || agent.status.Space != "" || agent.status.ConfigError != "" {
		t.Fatalf("stale fields retained: %v", agent.status)
	}
	if !bytes.Equal(agent.status.ProtoReflect().GetUnknown(), status.ProtoReflect().GetUnknown()) {
		t.Fatal("opaque protocol fields dropped")
	}
	status.Space = "mutated"
	if agent.status.Space != "" {
		t.Fatal("snapshot aliases sender memory")
	}
}
