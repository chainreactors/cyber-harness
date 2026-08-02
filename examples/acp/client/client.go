package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
)

// Client is a minimal browser-peer AOP client: natural language in, streamed
// events out. One WebSocket carries request/reply envelopes correlated by
// ReplyTo and event subscriptions keyed by the watch envelope ID.
type Client struct {
	conn *websocket.Conn

	mu      sync.Mutex
	pending map[string]chan protobuf.Message
	watches map[string]chan *aop.Event
	done    chan struct{}
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Dial connects to the hub's AOP WebSocket. serverURL is http(s)://host:port;
// token is sent as a Bearer credential on the upgrade.
func Dial(ctx context.Context, serverURL, wsPath, token string) (*Client, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	if wsPath == "" {
		wsPath = "/api/aop/application/ws"
	}
	u.Path = wsPath
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", u.Redacted(), err)
	}
	c := &Client{
		conn:    conn,
		pending: map[string]chan protobuf.Message{},
		watches: map[string]chan *aop.Event{},
		done:    make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) readLoop() {
	defer close(c.done)
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.mu.Lock()
			for id, ch := range c.pending {
				delete(c.pending, id)
				close(ch)
			}
			for id, ch := range c.watches {
				delete(c.watches, id)
				close(ch)
			}
			c.mu.Unlock()
			return
		}
		envelope := new(aop.Envelope)
		if err := protobuf.Unmarshal(data, envelope); err != nil {
			continue
		}
		message, err := aop.Unwrap(envelope)
		if err != nil {
			continue
		}
		c.mu.Lock()
		if core, ok := message.(*aop.ProtocolMessage); ok {
			if event := core.GetEvent(); event != nil {
				if ch, ok := c.watches[envelope.GetReplyTo()]; ok {
					ch <- event
				}
				c.mu.Unlock()
				continue
			}
		}
		if ch, ok := c.pending[envelope.GetReplyTo()]; ok {
			delete(c.pending, envelope.GetReplyTo())
			ch <- message
			close(ch)
		}
		c.mu.Unlock()
	}
}

// call sends one request envelope and waits for the correlated reply.
func (c *Client) call(ctx context.Context, message protobuf.Message) (protobuf.Message, error) {
	id := newID()
	envelope, err := aop.Wrap(id, "", message)
	if err != nil {
		return nil, err
	}
	ch := make(chan protobuf.Message, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	data, err := protobuf.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	err = c.conn.WriteMessage(websocket.BinaryMessage, data)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case reply, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("connection closed while waiting for reply")
		}
		if core, ok := reply.(*aop.ProtocolMessage); ok {
			if perr := core.GetProtocolError(); perr != nil {
				return nil, fmt.Errorf("%s: %s", perr.GetCode(), perr.GetMessage())
			}
		}
		return reply, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// OpenSession opens a chat session on the given agent node.
func (c *Client) OpenSession(ctx context.Context, nodeID, title string) (*aop.Session, error) {
	reply, err := c.call(ctx, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{
		OpenSessionRequest: &aop.OpenSessionRequest{NodeId: nodeID, Title: title},
	}})
	if err != nil {
		return nil, err
	}
	response := reply.(*aop.ProtocolMessage).GetOpenSessionResponse()
	if rejected := response.GetRejected(); rejected != nil {
		return nil, fmt.Errorf("open session rejected %s: %s", rejected.GetCode(), rejected.GetMessage())
	}
	return response.GetAccepted(), nil
}

// RunTurn submits one natural-language turn. Events stream via Watch.
func (c *Client) RunTurn(ctx context.Context, sessionID, text string) (*aop.TurnReceipt, error) {
	reply, err := c.call(ctx, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnRequest{
		RunTurnRequest: &aop.RunTurnRequest{
			SessionId: sessionID,
			Input:     &aop.Message{Role: "user", Content: []*aop.Content{aop.Text(text)}},
		},
	}})
	if err != nil {
		return nil, err
	}
	response := reply.(*aop.ProtocolMessage).GetRunTurnResponse()
	if rejected := response.GetRejected(); rejected != nil {
		return nil, fmt.Errorf("run turn rejected %s: %s", rejected.GetCode(), rejected.GetMessage())
	}
	return response.GetAccepted(), nil
}

// Watch subscribes to the session event stream. The returned channel closes
// when the connection drops or the hub ends the subscription.
func (c *Client) Watch(sessionID, afterCursor string) (<-chan *aop.Event, error) {
	id := newID()
	envelope, err := aop.Wrap(id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_WatchEventsRequest{
		WatchEventsRequest: &aop.WatchEventsRequest{SessionId: sessionID, AfterCursor: afterCursor},
	}})
	if err != nil {
		return nil, err
	}
	ch := make(chan *aop.Event, 64)
	c.mu.Lock()
	c.watches[id] = ch
	c.mu.Unlock()
	data, err := protobuf.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	err = c.conn.WriteMessage(websocket.BinaryMessage, data)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return ch, nil
}

// CloseSession ends the session on the hub.
func (c *Client) CloseSession(ctx context.Context, sessionID string) error {
	_, err := c.call(ctx, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionRequest{
		CloseSessionRequest: &aop.CloseSessionRequest{SessionId: sessionID},
	}})
	return err
}

func (c *Client) Close() error {
	err := c.conn.Close()
	<-c.done
	return err
}
