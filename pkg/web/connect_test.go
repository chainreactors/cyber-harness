package web

import (
	"connectrpc.com/connect"
	"context"
	"fmt"
	aop "github.com/chainreactors/aiscan/aop"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
	"net/http/httptest"
	"testing"
)

func TestConnectHandlerSupportsConnectGRPCWebAndGRPC(t *testing.T) {
	service := NewService(ServiceConfig{})
	defer service.Close()

	server := httptest.NewUnstartedServer(NewConnectHandler("", service))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	tests := []struct {
		name string
		opts []connect.ClientOption
	}{
		{name: "connect"},
		{name: "grpc-web", opts: []connect.ClientOption{connect.WithGRPCWeb()}},
		{name: "grpc", opts: []connect.ClientOption{connect.WithGRPC()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := rpc.NewSystemServiceClient(server.Client(), server.URL, test.opts...)
			response, err := client.GetStatus(context.Background(), connect.NewRequest(&types.GetStatusRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			if response.Msg.GetStatus() == nil {
				t.Fatal("status is missing")
			}
		})
	}
}

func TestAOPServiceUsesSharedEnvelopeStreamOverConnectAndGRPC(t *testing.T) {
	service := NewService(ServiceConfig{})
	pool := NewAgentPool(service.Hub())
	service.SetAgentPool(pool)
	defer service.Close()

	server := httptest.NewUnstartedServer(NewConnectHandler("", service))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	tests := []struct {
		name string
		opts []connect.ClientOption
	}{
		{name: "connect"},
		{name: "grpc", opts: []connect.ClientOption{connect.WithGRPC()}},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := rpc.NewAOPServiceClient(server.Client(), server.URL, test.opts...)
			stream := client.Connect(ctx)
			nodeID := fmt.Sprintf("agent-%d", index)
			envelope, err := aop.Wrap("hello-1", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentHello{AgentHello: &aop.AgentHello{
				NodeId: nodeID,
				Name:   nodeID,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(envelope); err != nil {
				t.Fatal(err)
			}
			response, err := stream.Receive()
			if err != nil {
				t.Fatal(err)
			}
			message, err := aop.Unwrap(response)
			if err != nil {
				t.Fatal(err)
			}
			core, ok := message.(*aop.ProtocolMessage)
			if !ok || core.GetAgentAccepted().GetNodeId() != nodeID {
				t.Fatalf("agent handshake response = %#v", message)
			}
			cancel()
			_ = stream.CloseRequest()
			_ = stream.CloseResponse()
		})
	}
}
