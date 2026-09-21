package ioa

import (
	"context"
	"fmt"

	"github.com/chainreactors/ioa/protocols"
)

// Reader is the query capability lent to hosts. It cannot register identities,
// change the command binding, subscribe, or close the extension's client.
type Reader interface {
	ListSpaces(context.Context) ([]protocols.SpaceInfo, error)
	ResolveSpace(context.Context, string) (protocols.SpaceInfo, error)
	ReadPublic(context.Context, string, protocols.ReadOptions) ([]protocols.Message, error)
	ListNodes(context.Context) ([]protocols.Node, error)
}

func query[T any](s *Service, ctx context.Context, call func(context.Context, sdkClient) (T, error)) (T, error) {
	var zero T
	if s == nil {
		return zero, fmt.Errorf("IOA client is unavailable")
	}
	if err := s.WaitReady(ctx); err != nil {
		return zero, err
	}
	s.mu.Lock()
	if s.closed || !s.loaded || s.client == nil {
		s.mu.Unlock()
		return zero, fmt.Errorf("IOA client is unavailable")
	}
	client, lifetime := s.client, s.lifetime
	s.queries.Add(1)
	s.mu.Unlock()
	defer s.queries.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifetime, cancel)
	defer stop()
	defer cancel()
	return call(ctx, client)
}

func (s *Service) ListSpaces(ctx context.Context) ([]protocols.SpaceInfo, error) {
	return query(s, ctx, func(ctx context.Context, client sdkClient) ([]protocols.SpaceInfo, error) {
		return client.ListSpaces(ctx)
	})
}

func (s *Service) ResolveSpace(ctx context.Context, nameOrID string) (protocols.SpaceInfo, error) {
	return query(s, ctx, func(ctx context.Context, client sdkClient) (protocols.SpaceInfo, error) {
		return client.ResolveSpace(ctx, nameOrID)
	})
}

func (s *Service) ReadPublic(ctx context.Context, spaceID string, options protocols.ReadOptions) ([]protocols.Message, error) {
	return query(s, ctx, func(ctx context.Context, client sdkClient) ([]protocols.Message, error) {
		return client.ReadPublic(ctx, spaceID, options)
	})
}

func (s *Service) ListNodes(ctx context.Context) ([]protocols.Node, error) {
	return query(s, ctx, func(ctx context.Context, client sdkClient) ([]protocols.Node, error) {
		return client.ListNodes(ctx)
	})
}

var _ Reader = (*Service)(nil)
