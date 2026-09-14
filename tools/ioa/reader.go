package ioa

import (
	"context"
	"fmt"

	ioaclient "github.com/chainreactors/ioa/client"
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

func query[T any](m *Runtime, ctx context.Context, call func(context.Context, *ioaclient.Client) (T, error)) (T, error) {
	var zero T
	if m == nil {
		return zero, fmt.Errorf("IOA client is unavailable")
	}
	if err := m.WaitReady(ctx); err != nil {
		return zero, err
	}
	m.mu.Lock()
	if m.closed || !m.loaded || m.client == nil {
		m.mu.Unlock()
		return zero, fmt.Errorf("IOA client is unavailable")
	}
	client, lifetime := m.client, m.lifetime
	m.queries.Add(1)
	m.mu.Unlock()
	defer m.queries.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifetime, cancel)
	defer stop()
	defer cancel()
	return call(ctx, client)
}

func (m *Runtime) ListSpaces(ctx context.Context) ([]protocols.SpaceInfo, error) {
	return query(m, ctx, func(ctx context.Context, client *ioaclient.Client) ([]protocols.SpaceInfo, error) {
		return client.ListSpaces(ctx)
	})
}

func (m *Runtime) ResolveSpace(ctx context.Context, nameOrID string) (protocols.SpaceInfo, error) {
	return query(m, ctx, func(ctx context.Context, client *ioaclient.Client) (protocols.SpaceInfo, error) {
		return client.ResolveSpace(ctx, nameOrID)
	})
}

func (m *Runtime) ReadPublic(ctx context.Context, spaceID string, options protocols.ReadOptions) ([]protocols.Message, error) {
	return query(m, ctx, func(ctx context.Context, client *ioaclient.Client) ([]protocols.Message, error) {
		return client.ReadPublic(ctx, spaceID, options)
	})
}

func (m *Runtime) ListNodes(ctx context.Context) ([]protocols.Node, error) {
	return query(m, ctx, func(ctx context.Context, client *ioaclient.Client) ([]protocols.Node, error) {
		return client.ListNodes(ctx)
	})
}

var _ Reader = (*Runtime)(nil)
