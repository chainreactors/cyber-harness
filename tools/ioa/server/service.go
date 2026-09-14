// Package server implements the owned IOA service independently of its host.
package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

type Config struct {
	AccessKey string
	Database  string
	// MCP enables the standalone server's additional MCP route. Web mounts
	// retain their existing REST/SSE surface unless explicitly selected.
	MCP bool
}

type Resource struct {
	Server    *Server
	config    Config
	store     *ioaserver.SQLiteStore
	closeOnce sync.Once
	closeErr  error
}

// Server publishes business operations. Only Resource can start or close it.
type Server struct {
	mu       sync.Mutex
	service  *ioaserver.Service
	handler  http.Handler
	key      string
	lifetime context.Context
	cancel   context.CancelFunc
	stopping bool
	active   int
	done     chan struct{}
}

func New(config Config) *Resource {
	return &Resource{config: config, Server: &Server{done: make(chan struct{})}}
}

func (r *Resource) Start(ctx context.Context) error {
	s := r.Server
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return fmt.Errorf("IOA server is closed")
	}
	if s.service != nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	database := r.config.Database
	if database == "" {
		database = ":memory:"
	}
	store, err := ioaserver.NewSQLiteStore(database)
	if err != nil {
		return err
	}
	r.store = store
	key := r.config.AccessKey
	if key == "" {
		key = protocols.NewToken()
	}
	service := ioaserver.NewService(store, key)
	handler := ioaserver.AuthMiddleware(service)(ioaserver.NewHandler(service))
	if r.config.MCP {
		handler = ioaserver.NewHTTPHandler(service)
	}
	s.service, s.handler, s.key = service, handler, key
	s.lifetime, s.cancel = context.WithCancel(context.Background())
	return ctx.Err()
}

func (s *Server) Handler() http.Handler { return s }

func (s *Server) AccessKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.key
}

func (s *Server) admit(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.service == nil || s.stopping {
		return nil, nil, fmt.Errorf("IOA server is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.active++
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.lifetime, cancel)
	return call, func() {
		stop()
		cancel()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.active--
		if s.stopping && s.active == 0 {
			close(s.done)
		}
	}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, release, err := s.admit(r.Context())
	if err != nil {
		http.Error(w, "IOA server is unavailable", http.StatusServiceUnavailable)
		return
	}
	defer release()
	s.handler.ServeHTTP(w, r.WithContext(ctx))
}

func (s *Server) RegisterIdentity(ctx context.Context, registration protocols.AuthRegister) (protocols.AuthResponse, error) {
	call, release, err := s.admit(ctx)
	if err != nil {
		return protocols.AuthResponse{}, err
	}
	defer release()
	return s.service.AuthRegister(call, registration)
}

func (r *Resource) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s := r.Server
	s.mu.Lock()
	if !s.stopping {
		s.stopping = true
		if s.cancel != nil {
			s.cancel()
		}
		if s.active == 0 {
			close(s.done)
		}
	}
	s.mu.Unlock()
	select {
	case <-s.done:
	default:
		select {
		case <-s.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.closeOnce.Do(func() {
		if r.store != nil {
			r.closeErr = r.store.Close()
		}
	})
	return r.closeErr
}
