package web

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
)

func TestConnectHandlerSupportsConnectGRPCWebAndGRPC(t *testing.T) {
	service := NewService(ServiceConfig{})
	defer service.Close()

	server := httptest.NewUnstartedServer(NewConnectHandler("", service, NewAgentPool(nil), nil))
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
