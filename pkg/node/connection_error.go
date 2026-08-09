package node

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"

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
	var unknownAuthorityErr x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var invalidCertificateErr x509.CertificateInvalidError
	if errors.As(err, &verificationErr) ||
		errors.As(err, &unknownAuthorityErr) ||
		errors.As(err, &hostnameErr) ||
		errors.As(err, &invalidCertificateErr) {
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

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Sprintf("DNS lookup failed for %s: %v", dnsErr.Name, err)
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Sprintf("TCP connection refused: %v", err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Sprintf("network timeout: %v", err)
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
