package node

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/gorilla/websocket"
)

type deadlineRecordingConn struct {
	net.Conn
	boundedReads  atomic.Int32
	boundedWrites atomic.Int32
}

func TestWebsocketLivenessUsesBoundedIntervals(t *testing.T) {
	if websocketPongWait <= 0 || websocketPingPeriod <= 0 || websocketWriteWait <= 0 {
		t.Fatal("websocket liveness intervals must be positive")
	}
	if websocketPingPeriod >= websocketPongWait {
		t.Fatalf("ping period %v must be shorter than pong wait %v", websocketPingPeriod, websocketPongWait)
	}
}

func (c *deadlineRecordingConn) SetReadDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		c.boundedReads.Add(1)
	}
	return c.Conn.SetReadDeadline(deadline)
}

func (c *deadlineRecordingConn) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		c.boundedWrites.Add(1)
	}
	return c.Conn.SetWriteDeadline(deadline)
}

func TestWebSocketStreamSetsReadAndWriteDeadlines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	recorded := make(chan *deadlineRecordingConn, 1)
	dialer := *websocket.DefaultDialer
	dialer.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		wrapped := &deadlineRecordingConn{Conn: conn}
		recorded <- wrapped
		return wrapped, nil
	}
	wsConn, response, err := dialer.DialContext(context.Background(), HTTPToWS(server.URL)+DefaultWSPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	stream, err := newWebSocketEnvelopeStream(wsConn, false)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	conn := <-recorded
	if conn.boundedReads.Load() == 0 {
		t.Fatal("websocket dial did not install a bounded read deadline")
	}
	writesBefore := conn.boundedWrites.Load()
	if err := stream.Send(&aop.Envelope{Id: "deadline-probe"}); err != nil {
		t.Fatal(err)
	}
	if conn.boundedWrites.Load() <= writesBefore {
		t.Fatal("application write did not install a bounded write deadline")
	}
}

func TestWebSocketStreamTimesOutSilentPeer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(250 * time.Millisecond)
	}))
	defer server.Close()

	stream, err := dialProtoWebSocket(context.Background(), connectionConfig{ServerURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("Recv error = %v, want timeout", err)
	}
}
