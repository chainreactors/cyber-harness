package node

import (
	"context"
	"io"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	trafficpb "github.com/chainreactors/aiscan/aop/traffic"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	protobuf "google.golang.org/protobuf/proto"
)

// namespaceReplyStream drives one connection through the handshake, delivers a
// single message addressed to an extra namespace, then blocks until the
// connection has written something back before ending the stream. Everything
// the connection sends is collected so a test can assert what reached the wire.
type namespaceReplyStream struct {
	helloID string
	recvs   int
	sent    chan *aop.Envelope
	payload *aop.Envelope
}

func (s *namespaceReplyStream) Send(envelope *aop.Envelope) error {
	if s.helloID == "" {
		s.helloID = envelope.GetId()
	}
	select {
	case s.sent <- envelope:
	default:
	}
	return nil
}

func (s *namespaceReplyStream) Recv() (*aop.Envelope, error) {
	s.recvs++
	switch s.recvs {
	case 1:
		return aop.MustWrap("accepted", s.helloID, &aop.ProtocolMessage{
			Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "runner-1"}},
		}), nil
	case 2:
		return s.payload, nil
	}
	// Hold the stream open long enough for the handler's reply to be written.
	time.Sleep(200 * time.Millisecond)
	return nil, io.EOF
}

// An ExtraNamespaces handler can only answer through the SendFunc the dispatch
// hands it: unlike the built-in namespaces, it has no closure over the
// connection's own sender. That argument used to be a discard, so a host
// namespace could be registered, could report success, and could never reply or
// stream — a runner whose capture was "on" looked exactly like a quiet target.
// This pins the reply path open.
func TestExtraNamespaceRepliesReachTheWire(t *testing.T) {
	stream := &namespaceReplyStream{
		sent: make(chan *aop.Envelope, 16),
		payload: aop.MustWrap("query-1", "", &trafficpb.ProtocolMessage{
			Message: &trafficpb.ProtocolMessage_Query{Query: &trafficpb.Query{State: true}},
		}),
	}

	cc := connectionConfig{
		Name:     "runner-1",
		NodeID:   "runner-1",
		Registry: commands.NewRegistry(),
		ExtraNamespaces: []func(*aop.NamespaceMux) error{
			func(mux *aop.NamespaceMux) error {
				return mux.Register(&trafficpb.ProtocolMessage{}, func(
					_ context.Context, envelope *aop.Envelope, _ protobuf.Message, send aop.SendFunc,
				) error {
					reply, err := aop.Wrap("reply-1", envelope.GetId(), &trafficpb.ProtocolMessage{
						Message: &trafficpb.ProtocolMessage_State{State: &trafficpb.State{
							Capture: &trafficpb.CaptureState{Capturing: true},
						}},
					})
					if err != nil {
						return err
					}
					return send(reply)
				})
			},
		},
	}

	if err := serveAgentConnection(context.Background(), cc, telemetry.NopLogger(), stream); err != io.EOF {
		t.Fatalf("serveAgentConnection error = %v, want EOF", err)
	}

	for {
		select {
		case envelope := <-stream.sent:
			message, err := aop.Unwrap(envelope)
			if err != nil {
				continue
			}
			value, ok := message.(*trafficpb.ProtocolMessage)
			if !ok {
				continue
			}
			if value.GetState().GetCapture().GetCapturing() {
				if envelope.GetReplyTo() != "query-1" {
					t.Fatalf("reply_to = %q, want query-1", envelope.GetReplyTo())
				}
				return
			}
		default:
			t.Fatal("the namespace handler's reply never reached the wire")
		}
	}
}
