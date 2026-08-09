package node

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/gorilla/websocket"
)

type deadlineRecordingConn struct {
	net.Conn
	boundedReads  atomic.Int32
	boundedWrites atomic.Int32
}

func TestDefaultWebsocketLivenessUsesBoundedIntervals(t *testing.T) {
	liveness := defaultWebsocketLiveness()
	if liveness.pongWait <= 0 || liveness.pingPeriod <= 0 || liveness.writeWait <= 0 {
		t.Fatalf("default liveness intervals must be positive: %+v", liveness)
	}
	if liveness.pingPeriod >= liveness.pongWait {
		t.Fatalf("ping period %v must be shorter than pong wait %v", liveness.pingPeriod, liveness.pongWait)
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
	stream, err := dialProtoWebSocket(context.Background(), connectionConfig{
		ServerURL: server.URL,
		Dialer:    &dialer,
		Liveness: websocketLiveness{
			pongWait:   time.Second,
			pingPeriod: 500 * time.Millisecond,
			writeWait:  time.Second,
		},
	})
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

func TestConnectGeneratedReconnectsWhenPeerStopsAnsweringHeartbeats(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	connected := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		first, message, err := readAgentEnvelope(conn, false)
		core, ok := message.(*aop.ProtocolMessage)
		if err != nil || !ok || core.GetAgentHello() == nil {
			return
		}
		if err := writeAgentEnvelope(conn, false, aop.MustWrap("accepted", first.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "silent-peer-runner"}}})); err != nil {
			return
		}
		connected <- struct{}{}
		// Gorilla only processes Ping frames while reading. This peer stops
		// reading after enrollment, so it intentionally sends no Pong responses.
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	overrideReconnectTiming(t, time.Second, func(int) time.Duration {
		return 10 * time.Millisecond
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- connectGenerated(ctx, connectionConfig{
			ServerURL: server.URL,
			Name:      "silent-peer-runner",
			NodeID:    "silent-peer-runner",
			Registry:  commands.NewRegistry(),
			Logger:    telemetry.NopLogger(),
			Liveness: websocketLiveness{
				pongWait:   80 * time.Millisecond,
				pingPeriod: 20 * time.Millisecond,
				writeWait:  40 * time.Millisecond,
			},
		})
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-connected:
		case <-time.After(time.Second):
			t.Fatalf("observed %d connections; heartbeat did not force reconnect", i)
		}
	}
	cancel()
	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("connection loop did not stop")
	}
}
