package node

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

type websocketHandshakeError struct {
	statusCode int
	cause      error
}

func (e *websocketHandshakeError) Error() string {
	return fmt.Sprintf("websocket handshake failed with HTTP %d: %v", e.statusCode, e.cause)
}

func (e *websocketHandshakeError) Unwrap() error { return e.cause }

func describeConnectionFailure(err error) string {
	if err == nil {
		return "connection closed without an error"
	}

	var verificationErr *tls.CertificateVerificationError
	if errors.As(err, &verificationErr) {
		return fmt.Sprintf("TLS certificate verification failed: %v; install a trusted certificate or explicitly enable insecure TLS for private deployments", err)
	}

	var handshakeErr *websocketHandshakeError
	if errors.As(err, &handshakeErr) {
		switch handshakeErr.statusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Sprintf("WebSocket authentication rejected (HTTP %d): check the runner token", handshakeErr.statusCode)
		case http.StatusNotFound:
			return fmt.Sprintf("WebSocket endpoint not found (HTTP %d): check the server URL and WebSocket path", handshakeErr.statusCode)
		default:
			return fmt.Sprintf("WebSocket handshake rejected (HTTP %d): %v", handshakeErr.statusCode, handshakeErr.cause)
		}
	}

	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		if closeErr.Text == "" {
			return fmt.Sprintf("WebSocket closed by peer (code %d)", closeErr.Code)
		}
		return fmt.Sprintf("WebSocket closed by peer (code %d: %s)", closeErr.Code, closeErr.Text)
	}
	return err.Error()
}
