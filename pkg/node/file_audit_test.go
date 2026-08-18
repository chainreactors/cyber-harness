package node

import (
	"context"
	"io"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
)

// fileAuditStream drives one connection through the handshake, delivers a
// Configure asking for observation, records an access, then holds the stream
// open long enough for the observation to be written.
type fileAuditStream struct {
	helloID string
	recvs   int
	sent    chan *aop.Envelope
	audit   *commands.FileAudit
}

func (s *fileAuditStream) Send(envelope *aop.Envelope) error {
	if s.helloID == "" {
		s.helloID = envelope.GetId()
	}
	select {
	case s.sent <- envelope:
	default:
	}
	return nil
}

func (s *fileAuditStream) Recv() (*aop.Envelope, error) {
	s.recvs++
	switch s.recvs {
	case 1:
		return aop.MustWrap("accepted", s.helloID, &aop.ProtocolMessage{
			Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "runner-1"}},
		}), nil
	case 2:
		return aop.MustWrap("configure-1", "", &filepb.ProtocolMessage{
			Message: &filepb.ProtocolMessage_Configure{Configure: &filepb.Configure{
				Watch: &filepb.WatchConfig{Enabled: true},
			}},
		}), nil
	case 3:
		// A tool touched a file while the connection was up.
		s.audit.Record(context.Background(), &filepb.Access{
			ToolId: "call-1",
			Op:     filepb.AccessOp_ACCESS_OP_EDIT,
			Source: filepb.AccessSource_ACCESS_SOURCE_TOOL,
			Path:   "/root/work/main.go",
		})
	}
	time.Sleep(200 * time.Millisecond)
	return nil, io.EOF
}

// The file namespace carries the audit trail as well as its RPCs. This pins the
// whole path open: a peer asks for observation, the node answers with its watch
// state, and every access recorded afterwards reaches the wire addressed to the
// tool call that produced it.
func TestFileAuditReachesTheWire(t *testing.T) {
	audit := commands.NewFileAudit()
	defer audit.Close()

	stream := &fileAuditStream{sent: make(chan *aop.Envelope, 32), audit: audit}
	cc := connectionConfig{
		Name:      "runner-1",
		NodeID:    "runner-1",
		Registry:  commands.NewRegistry(),
		FileAudit: audit,
	}

	if err := serveAgentConnection(context.Background(), cc, telemetry.NopLogger(), stream); err != io.EOF {
		t.Fatalf("serveAgentConnection error = %v, want EOF", err)
	}

	var sawState, sawAccess bool
	for {
		select {
		case envelope := <-stream.sent:
			message, err := aop.Unwrap(envelope)
			if err != nil {
				continue
			}
			value, ok := message.(*filepb.ProtocolMessage)
			if !ok {
				continue
			}
			if state := value.GetState(); state != nil {
				if !state.GetWatching() {
					t.Fatal("the node answered Configure by saying it is not watching")
				}
				if envelope.GetReplyTo() != "configure-1" {
					t.Fatalf("state reply_to = %q, want configure-1", envelope.GetReplyTo())
				}
				sawState = true
			}
			if access := value.GetAccess(); access != nil {
				if access.GetPath() != "/root/work/main.go" {
					t.Fatalf("path = %q", access.GetPath())
				}
				// Addressed to the call that produced it, so a consumer can
				// attribute the observation without a second lookup.
				if envelope.GetReplyTo() != "call-1" {
					t.Fatalf("access reply_to = %q, want call-1", envelope.GetReplyTo())
				}
				if access.GetId() == "" || access.GetTimestamp() == nil {
					t.Fatalf("the audit must stamp identity and timing: %+v", access)
				}
				sawAccess = true
			}
		default:
			if !sawState {
				t.Fatal("the watch state never reached the wire")
			}
			if !sawAccess {
				t.Fatal("the recorded access never reached the wire")
			}
			return
		}
	}
}

// Without an audit the namespace still answers, so a peer learns that this node
// reports nothing rather than waiting for a stream that will never start.
func TestFileConfigureWithoutAnAuditStillAnswers(t *testing.T) {
	stream := &fileAuditStream{sent: make(chan *aop.Envelope, 32), audit: commands.NewFileAudit()}
	defer stream.audit.Close()
	cc := connectionConfig{Name: "runner-1", NodeID: "runner-1", Registry: commands.NewRegistry()}

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
			value, ok := message.(*filepb.ProtocolMessage)
			if !ok {
				continue
			}
			if state := value.GetState(); state != nil {
				if state.GetWatching() {
					t.Fatal("a node with no audit must not claim to be watching")
				}
				return
			}
			if value.GetAccess() != nil {
				t.Fatal("a node with no audit must not stream observations")
			}
		default:
			t.Fatal("the watch state never reached the wire")
		}
	}
}
