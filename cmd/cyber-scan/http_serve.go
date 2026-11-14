package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// serveManagedHTTP joins serving and cleanup on every exit path. Resource
// cleanup cancels streaming handlers before HTTP Shutdown waits for them.
func serveManagedHTTP(ctx context.Context, srv *http.Server, listener net.Listener, closeResources func(context.Context) error) error {
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	var serveErr error
	exited := false
	select {
	case serveErr = <-served:
		exited = true
	case <-ctx.Done():
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resourceErr := closeResources(cleanup)
	httpErr := srv.Shutdown(cleanup)
	if httpErr != nil {
		_ = srv.Close()
	}
	if !exited {
		serveErr = <-served
	}
	if errors.Is(resourceErr, context.Canceled) || errors.Is(resourceErr, context.DeadlineExceeded) {
		// Forced transport closure can release a handler blocked in a write.
		retry, stop := context.WithTimeout(context.Background(), 5*time.Second)
		resourceErr = errors.Join(resourceErr, closeResources(retry))
		stop()
	}
	if errors.Is(serveErr, http.ErrServerClosed) || errors.Is(serveErr, net.ErrClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, resourceErr, httpErr)
}
