package node

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/gorilla/websocket"
)

type rejectingEnvelopeStream struct {
	hello   *aop.Envelope
	replyTo string
}

func (s *rejectingEnvelopeStream) Send(envelope *aop.Envelope) error {
	s.hello = envelope
	return nil
}

func (s *rejectingEnvelopeStream) Recv() (*aop.Envelope, error) {
	replyTo := s.replyTo
	if replyTo == "" {
		replyTo = s.hello.GetId()
	}
	return aop.MustWrap("rejected", replyTo, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_ProtocolError{ProtocolError: &aop.ProtocolError{
		Code: "ALREADY_EXISTS", Message: "runner ID is already connected by another process",
	}}}), nil
}

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

func (*warningChannelLogger) SetOutput(io.Writer)       {}
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

	logger := &warningChannelLogger{warnings: make(chan string, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- connect(ctx, connectionConfig{
			ServerURL: server.URL,
			Logger:    logger,
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
	for _, test := range []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "WebSocket authentication rejected (HTTP 401)"},
		{http.StatusForbidden, "WebSocket authentication rejected (HTTP 403)"},
		{http.StatusNotFound, "WebSocket endpoint not found (HTTP 404)"},
		{http.StatusServiceUnavailable, "WebSocket handshake rejected (HTTP 503)"},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "connection rejected", test.status)
			}))
			defer server.Close()
			_, err := dialProtoWebSocket(context.Background(), connectionConfig{ServerURL: server.URL})
			if err == nil || !errors.Is(err, websocket.ErrBadHandshake) {
				t.Fatalf("dial error = %v, want bad handshake", err)
			}
			if diagnostic := describeConnectionFailure(err); !strings.Contains(diagnostic, test.want) {
				t.Fatalf("diagnostic = %q, want %q", diagnostic, test.want)
			}
		})
	}
}

func TestServeAgentConnectionPreservesEnrollmentRejection(t *testing.T) {
	err := serveAgentConnection(
		context.Background(),
		connectionConfig{Name: "runner-1", NodeID: "runner-1", Events: coreevents.New()},
		telemetry.NopLogger(),
		new(rejectingEnvelopeStream),
	)
	if err == nil {
		t.Fatal("enrollment rejection unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "ALREADY_EXISTS") || !strings.Contains(err.Error(), "already connected") {
		t.Fatalf("enrollment error lost the server reason: %v", err)
	}
}

func TestServeAgentConnectionRejectsUncorrelatedEnrollmentError(t *testing.T) {
	err := serveAgentConnection(
		context.Background(),
		connectionConfig{Name: "runner-1", NodeID: "runner-1", Events: coreevents.New()},
		telemetry.NopLogger(),
		&rejectingEnvelopeStream{replyTo: "another-request"},
	)
	if err == nil || !strings.Contains(err.Error(), "expected AOP enrollment response") {
		t.Fatalf("uncorrelated response was accepted as an enrollment rejection: %v", err)
	}
}
