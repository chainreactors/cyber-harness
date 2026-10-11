package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chainreactors/cyber/core/resource"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// Connection owns a client and, for stdio, its direct child process. It does
// not own an HTTP server or any engine/database managed by the remote server.
type Connection struct {
	name   string
	config ServerConfig
	client *client.Client
	http   *http.Transport
	killed atomic.Bool
	nextID atomic.Uint64

	mu       sync.Mutex
	cancel   context.CancelFunc
	started  bool
	closing  bool
	work     sync.WaitGroup
	done     chan struct{}
	closeErr error
}

// New constructs a dormant connection; Start performs all external I/O.
func New(name string, config ServerConfig) (*Connection, error) {
	if err := config.Validate(name); err != nil {
		return nil, err
	}
	return &Connection{name: name, config: config.Clone()}, nil
}

func (c *Connection) Start(init, lifetime context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started || c.closing {
		return fmt.Errorf("MCP server %s: connection already started or closed", c.name)
	}
	if err := init.Err(); err != nil {
		return err
	}
	c.started = true
	// Scope revokes and drains commands before Close. Keep transport shutdown
	// under this owner's control so stdio can first exit on EOF; binding the
	// child directly to scope cancellation races process exit on Windows.
	life, cancel := context.WithCancel(context.WithoutCancel(lifetime))
	c.cancel = cancel
	if c.config.Command != "" {
		stdio := transport.NewStdioWithOptions(c.config.Command, nil, c.config.Args,
			transport.WithCommandLogger(quietLogger{}),
			transport.WithCommandFunc(func(ctx context.Context, command string, _ []string, args []string) (*exec.Cmd, error) {
				cmd := exec.CommandContext(ctx, command, args...)
				cmd.Dir = c.config.Cwd
				cmd.Env = os.Environ()
				keys := make([]string, 0, len(c.config.Env))
				for key := range c.config.Env {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					cmd.Env = append(cmd.Env, key+"="+c.config.Env[key])
				}
				cmd.Cancel = func() error {
					err := cmd.Process.Kill()
					if err == nil {
						c.killed.Store(true)
					}
					return err
				}
				cmd.WaitDelay = 2 * time.Second
				return cmd, nil
			}))
		c.client = client.NewClient(stdio)
	} else {
		// Own a transport rather than assuming an embedding application's
		// mutable http.DefaultTransport has the standard concrete type.
		c.http = &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DialContext:       (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2: true, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second, ExpectContinueTimeout: time.Second,
		}
		httpClient := &http.Client{
			// The wrapper also authenticates the SDK's session DELETE request.
			Transport:     headerTransport{base: c.http, headers: c.config.Headers},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		var err error
		c.client, err = client.NewStreamableHttpClient(c.config.URL,
			transport.WithHTTPBasicClient(httpClient), transport.WithHTTPLogger(quietLogger{}))
		if err != nil {
			return c.diagnostic(err)
		}
	}
	if err := c.client.Start(life); err != nil {
		return c.diagnostic(err)
	}
	if stdio, ok := c.client.GetTransport().(*transport.Stdio); ok && stdio.Stderr() != nil {
		// Never leave a full stderr pipe blocking the protocol process. The
		// upstream application owns diagnostics; we do not persist its output.
		c.work.Add(1)
		go func() { defer c.work.Done(); _, _ = io.Copy(io.Discard, stdio.Stderr()) }()
	}
	ctx, stop := context.WithTimeout(init, timeout(c.config.StartupTimeoutSeconds, 30*time.Second))
	defer stop()
	// Track SDK work even if a stdio write outlives the request deadline.
	finished := make(chan error, 1)
	c.work.Add(1)
	go func() {
		defer c.work.Done()
		_, err := c.client.Initialize(ctx, mcpsdk.InitializeRequest{Params: mcpsdk.InitializeParams{
			ProtocolVersion: mcpsdk.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpsdk.Implementation{Name: "cyber-harness", Version: "mcp-ext"},
		}})
		finished <- err
	}()
	select {
	case err := <-finished:
		return c.diagnostic(err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Connection) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := mcpsdk.NewRequestId(fmt.Sprintf("cyber-mcp-%d", c.nextID.Add(1)))
	finished := make(chan requestResult, 1)
	if !c.launch(func() {
		response, err := c.client.GetTransport().SendRequest(ctx, transport.JSONRPCRequest{
			JSONRPC: mcpsdk.JSONRPC_VERSION, ID: id, Method: method, Params: params,
		})
		finished <- requestResult{response, err}
	}) {
		return nil, fmt.Errorf("MCP server %s: connection unavailable", c.name)
	}
	select {
	case result := <-finished:
		if result.err != nil {
			if ctx.Err() != nil {
				c.notifyCancelled(id)
			}
			return nil, c.diagnostic(result.err)
		}
		if result.response == nil {
			return nil, fmt.Errorf("MCP server %s: empty RPC response", c.name)
		}
		if result.response.ID.String() != id.String() {
			return nil, fmt.Errorf("MCP server %s: mismatched RPC response ID", c.name)
		}
		if result.response.Error != nil {
			return nil, c.diagnostic(&RPCError{Details: *result.response.Error})
		}
		return result.response.Result, nil
	case <-ctx.Done():
		c.notifyCancelled(id)
		return nil, ctx.Err()
	}
}

type requestResult struct {
	response *transport.JSONRPCResponse
	err      error
}

func (c *Connection) launch(run func()) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started || c.closing || c.client == nil {
		return false
	}
	c.work.Add(1)
	go func() { defer c.work.Done(); run() }()
	return true
}

const cancellationMethod = "notifications/cancelled" //nolint:misspell // MCP specifies this wire method spelling.

func (c *Connection) notifyCancelled(id mcpsdk.RequestId) {
	c.launch(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.client.GetTransport().SendNotification(ctx, mcpsdk.JSONRPCNotification{
			JSONRPC: mcpsdk.JSONRPC_VERSION,
			Notification: mcpsdk.Notification{Method: cancellationMethod, Params: mcpsdk.NotificationParams{
				AdditionalFields: map[string]any{"requestId": id, "reason": "caller canceled or timed out"},
			}},
		})
	})
}

// Close retains its asynchronous cleanup attempt for deadline-bound retries.
func (c *Connection) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.done == nil {
		c.closing = true
		c.done = make(chan struct{})
		go func() {
			var err error
			if c.client != nil {
				finished := make(chan error, 1)
				go func() { finished <- c.client.Close() }()
				// EOF is the first shutdown signal. Terminate an uncooperative
				// direct child after the grace period, without touching servers
				// started independently of this connection.
				select {
				case err = <-finished:
				case <-time.After(200 * time.Millisecond):
					if c.cancel != nil {
						c.cancel()
					}
					err = <-finished
				}
			}
			if c.cancel != nil {
				c.cancel()
			}
			c.work.Wait()
			if c.http != nil {
				c.http.CloseIdleConnections()
			}
			var exit *exec.ExitError
			if c.killed.Load() && errors.As(err, &exit) {
				err = nil // Expected exit of the direct child we just terminated.
			}
			c.closeErr = c.diagnostic(err)
			close(c.done)
		}()
	}
	done := c.done
	c.mu.Unlock()
	select {
	case <-done:
		return c.closeErr
	default:
	}
	select {
	case <-done:
		return c.closeErr
	case <-ctx.Done():
		return errors.Join(resource.ErrCloseIncomplete, ctx.Err())
	}
}

// RPCError preserves the SDK's protocol error code and data for callers.
type RPCError struct{ Details mcpsdk.JSONRPCErrorDetails }

func (e *RPCError) Error() string {
	data, _ := json.Marshal(e.Details.Data)
	return fmt.Sprintf("MCP RPC error %d: %s (data: %s)", e.Details.Code, e.Details.Message, data)
}

type diagnosticError struct {
	message string
	cause   error
}

func (e diagnosticError) Error() string { return e.message }
func (e diagnosticError) Unwrap() error { return e.cause }

func (c *Connection) diagnostic(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, value := range c.config.Headers {
		if value != "" {
			message = strings.ReplaceAll(message, value, "[redacted MCP header]")
		}
	}
	return diagnosticError{message: "MCP server " + c.name + ": " + message, cause: err}
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	for key, value := range t.headers {
		copy.Header.Set(key, value)
	}
	return t.base.RoundTrip(copy)
}

type quietLogger struct{}

func (quietLogger) Infof(string, ...any)  {}
func (quietLogger) Errorf(string, ...any) {}
