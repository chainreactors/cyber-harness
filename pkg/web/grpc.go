package web

import (
	"context"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
	scanpb "github.com/chainreactors/aiscan/aop/aiscan/scan"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/pkg/web/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func NewGRPCServer(accessKey string, service *Service, pool *AgentPool) *grpc.Server {
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(grpcUnaryAuth(accessKey)),
		grpc.ChainStreamInterceptor(grpcStreamAuth(accessKey)),
	)
	aop.RegisterChatServiceServer(server, NewAOPChatServer(service))
	scanpb.RegisterScanServiceServer(server, newGRPCScanServer(service))
	transport.RegisterAgentTransportServiceServer(server, NewAgentTransportServer(pool))
	return server
}

func grpcUnaryAuth(accessKey string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !grpcAuthenticated(ctx, accessKey) {
			return nil, status.Error(codes.Unauthenticated, "invalid or missing access key")
		}
		return handler(ctx, req)
	}
}

func grpcStreamAuth(accessKey string) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !grpcAuthenticated(stream.Context(), accessKey) {
			return status.Error(codes.Unauthenticated, "invalid or missing access key")
		}
		return handler(srv, stream)
	}
}

func grpcAuthenticated(ctx context.Context, accessKey string) bool {
	if accessKey == "" {
		return true
	}
	values, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	for _, value := range values.Get("authorization") {
		parts := strings.Fields(value)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && auth.AccessKeyMatches(accessKey, parts[1]) {
			return true
		}
	}
	return false
}
