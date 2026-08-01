// Package aiscan provides stable client facades over AIScan's generated
// protobuf service groups.
package aiscan

import (
	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	chatpb "github.com/chainreactors/aiscan/aop/aiscan/chat"
	"github.com/chainreactors/aiscan/aop/aiscan/chat/chatconnect"
	scanpb "github.com/chainreactors/aiscan/aop/aiscan/scan"
	"github.com/chainreactors/aiscan/aop/aiscan/scan/scanconnect"
	"github.com/chainreactors/aiscan/aop/aopconnect"
	"google.golang.org/grpc"
)

// Client groups the public ConnectRPC APIs while reusing one HTTP transport,
// base URL and option set.
type Client struct {
	Chat     aopconnect.ChatServiceClient
	Sessions chatconnect.SessionServiceClient
	Scans    scanconnect.ScanServiceClient
}

func NewClient(httpClient connect.HTTPClient, baseURL string, options ...connect.ClientOption) *Client {
	return &Client{
		Chat:     aopconnect.NewChatServiceClient(httpClient, baseURL, options...),
		Sessions: chatconnect.NewSessionServiceClient(httpClient, baseURL, options...),
		Scans:    scanconnect.NewScanServiceClient(httpClient, baseURL, options...),
	}
}

// GRPCClient groups the same public APIs over one native gRPC connection.
type GRPCClient struct {
	Chat     aop.ChatServiceClient
	Sessions chatpb.SessionServiceClient
	Scans    scanpb.ScanServiceClient
}

func NewGRPCClient(connection grpc.ClientConnInterface) *GRPCClient {
	return &GRPCClient{
		Chat:     aop.NewChatServiceClient(connection),
		Sessions: chatpb.NewSessionServiceClient(connection),
		Scans:    scanpb.NewScanServiceClient(connection),
	}
}
