package node

import (
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/gorilla/websocket"
)

func describeConnectionFailure(err error) string {
	if err == nil {
		return "connection closed without an error"
	}

	var verificationErr *tls.CertificateVerificationError
	if errors.As(err, &verificationErr) {
		return fmt.Sprintf("TLS certificate verification failed: %v; install a trusted certificate or explicitly enable insecure TLS for private deployments", err)
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
