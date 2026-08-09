package node

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/gorilla/websocket"
)

func TestDescribeConnectionFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "TLS verification",
			err: &tls.CertificateVerificationError{
				Err: errors.New("x509: certificate signed by unknown authority"),
			},
			want: "TLS certificate verification failed",
		},
		{
			name: "authentication handshake",
			err: &websocketHandshakeError{
				statusCode: http.StatusUnauthorized,
				cause:      websocket.ErrBadHandshake,
			},
			want: "WebSocket authentication rejected (HTTP 401)",
		},
		{
			name: "missing endpoint",
			err: &websocketHandshakeError{
				statusCode: http.StatusNotFound,
				cause:      websocket.ErrBadHandshake,
			},
			want: "WebSocket endpoint not found (HTTP 404)",
		},
		{
			name: "DNS",
			err:  &net.DNSError{Name: "missing.example", IsNotFound: true},
			want: "DNS lookup failed for missing.example",
		},
		{
			name: "connection refused",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
			want: "TCP connection refused",
		},
		{
			name: "network timeout",
			err:  context.DeadlineExceeded,
			want: "network timeout",
		},
		{
			name: "remote close",
			err:  &websocket.CloseError{Code: websocket.CloseAbnormalClosure, Text: "unexpected EOF"},
			want: "WebSocket closed by peer (code 1006: unexpected EOF)",
		},
		{
			name: "fallback",
			err:  errors.New("transport failed"),
			want: "transport failed",
		},
		{
			name: "missing error",
			want: "connection closed without an error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := describeConnectionFailure(tt.err); !strings.Contains(got, tt.want) {
				t.Fatalf("describeConnectionFailure() = %q, want substring %q", got, tt.want)
			}
		})
	}
}

type warningChannelLogger struct {
	warnings chan string
}

func (*warningChannelLogger) Debugf(string, ...any)     {}
func (*warningChannelLogger) Infof(string, ...any)      {}
func (*warningChannelLogger) Errorf(string, ...any)     {}
func (*warningChannelLogger) Importantf(string, ...any) {}
func (l *warningChannelLogger) Warnf(format string, args ...any) {
	select {
	case l.warnings <- fmt.Sprintf(format, args...):
	default:
	}
}

func TestConnectGeneratedDiagnosesTLSVerificationFailure(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("request unexpectedly reached the HTTP handler")
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()

	dialer := *websocket.DefaultDialer
	dialer.TLSClientConfig = nil
	logger := &warningChannelLogger{warnings: make(chan string, 8)}
	overrideReconnectTiming(t, time.Second, func(int) time.Duration {
		return 10 * time.Millisecond
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- connectGenerated(ctx, connectionConfig{
			ServerURL: server.URL,
			Registry:  commands.NewRegistry(),
			Logger:    logger,
			Dialer:    &dialer,
		})
	}()

	select {
	case warning := <-logger.warnings:
		if !strings.Contains(warning, "TLS certificate verification failed") {
			t.Fatalf("warning = %q", warning)
		}
		if !strings.Contains(warning, "trusted certificate") {
			t.Fatalf("warning lacks operator guidance: %q", warning)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TLS diagnostic")
	}
	cancel()
	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("connection loop did not stop")
	}
}

func TestDialProtoWebSocketPreservesHandshakeStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid runner token", http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := dialProtoWebSocket(context.Background(), connectionConfig{ServerURL: server.URL})
	if err == nil {
		t.Fatal("dial unexpectedly succeeded")
	}
	diagnostic := describeConnectionFailure(err)
	if !strings.Contains(diagnostic, "WebSocket authentication rejected (HTTP 401)") {
		t.Fatalf("diagnostic = %q", diagnostic)
	}
}
