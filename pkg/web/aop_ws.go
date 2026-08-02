package web

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
	ptypb "github.com/chainreactors/aiscan/aop/pty"
	types "github.com/chainreactors/aiscan/pkg/types"
	terminalcodec "github.com/chainreactors/aiscan/pkg/web/terminal"
	"github.com/chainreactors/utils/pty"
	protobuf "google.golang.org/protobuf/proto"
)

type browserPTYRoute struct {
	nodeID      string
	unsubscribe func()
}

func (s *Service) serveBrowserAOP(parent context.Context, stream aop.EnvelopeStream, first *aop.Envelope) error {
	if s == nil || s.agents == nil || stream == nil {
		return fmt.Errorf("browser AOP connection is unavailable")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	sendCh := make(chan *aop.Envelope, 128)
	writeErr := make(chan error, 1)
	go func() {
		for {
			select {
			case envelope := <-sendCh:
				if envelope == nil {
					continue
				}
				if err := stream.Send(envelope); err != nil {
					select {
					case writeErr <- err:
					default:
					}
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	sendEnvelope := func(envelope *aop.Envelope) error {
		select {
		case sendCh <- envelope:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	send := func(replyTo, cursor string, message protobuf.Message) error {
		envelope, err := aop.Wrap(generateID(), replyTo, message)
		if err != nil {
			return err
		}
		envelope.DeliveryCursor = cursor
		return sendEnvelope(envelope)
	}
	fail := func(replyTo, code string, err error) {
		if err == nil {
			return
		}
		_ = send(replyTo, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_ProtocolError{ProtocolError: &aop.ProtocolError{Code: code, Message: err.Error()}}})
	}

	var stateMu sync.Mutex
	subscriptions := make(map[string]context.CancelFunc)
	ptyRoutes := make(map[string]browserPTYRoute)
	setSubscription := func(id string, subscriptionCancel context.CancelFunc) {
		stateMu.Lock()
		if previous := subscriptions[id]; previous != nil {
			previous()
		}
		subscriptions[id] = subscriptionCancel
		stateMu.Unlock()
	}
	cancelSubscription := func(id string) {
		stateMu.Lock()
		if subscriptionCancel := subscriptions[id]; subscriptionCancel != nil {
			delete(subscriptions, id)
			subscriptionCancel()
		}
		stateMu.Unlock()
	}
	removePTY := func(streamID string, detach bool) {
		stateMu.Lock()
		route, ok := ptyRoutes[streamID]
		if ok {
			delete(ptyRoutes, streamID)
		}
		stateMu.Unlock()
		if !ok {
			return
		}
		route.unsubscribe()
		if detach {
			s.agents.CloseTerminal(route.nodeID, streamID)
		}
	}
	defer func() {
		stateMu.Lock()
		cancels := make([]context.CancelFunc, 0, len(subscriptions))
		routes := make(map[string]browserPTYRoute, len(ptyRoutes))
		for _, subscriptionCancel := range subscriptions {
			cancels = append(cancels, subscriptionCancel)
		}
		for streamID, route := range ptyRoutes {
			routes[streamID] = route
		}
		stateMu.Unlock()
		for _, subscriptionCancel := range cancels {
			subscriptionCancel()
		}
		for streamID, route := range routes {
			route.unsubscribe()
			s.agents.CloseTerminal(route.nodeID, streamID)
		}
	}()

	sessions := s.api.Sessions
	scans := s.api.Scans
	handle := func(envelope *aop.Envelope) {
		message, err := aop.Unwrap(envelope)
		if err != nil {
			fail(envelope.GetId(), "INVALID_PAYLOAD", err)
			return
		}
		switch value := message.(type) {
		case *aop.ProtocolMessage:
			switch payload := value.Message.(type) {
			case *aop.ProtocolMessage_OpenSessionRequest:
				go func() {
					response, err := sessions.OpenSession(ctx, envelope.Id, payload.OpenSessionRequest)
					if err != nil {
						fail(envelope.Id, "OPEN_SESSION_FAILED", err)
						return
					}
					_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionResponse{OpenSessionResponse: response}})
				}()
			case *aop.ProtocolMessage_RunTurnRequest:
				go func() {
					response, err := sessions.RunTurn(ctx, envelope.Id, payload.RunTurnRequest)
					if err != nil {
						fail(envelope.Id, "RUN_TURN_FAILED", err)
						return
					}
					_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnResponse{RunTurnResponse: response}})
				}()
			case *aop.ProtocolMessage_CancelTurnRequest:
				go func() {
					response, err := sessions.CancelTurn(ctx, envelope.Id, payload.CancelTurnRequest)
					if err != nil {
						fail(envelope.Id, "CANCEL_TURN_FAILED", err)
						return
					}
					_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelTurnResponse{CancelTurnResponse: response}})
				}()
			case *aop.ProtocolMessage_CloseSessionRequest:
				go func() {
					response, err := sessions.CloseSession(ctx, envelope.Id, payload.CloseSessionRequest)
					if err != nil {
						fail(envelope.Id, "CLOSE_SESSION_FAILED", err)
						return
					}
					_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionResponse{CloseSessionResponse: response}})
				}()
			case *aop.ProtocolMessage_ListEventsRequest:
				go func() {
					response, err := sessions.ListEvents(ctx, payload.ListEventsRequest)
					if err != nil {
						fail(envelope.Id, "LIST_EVENTS_FAILED", err)
						return
					}
					_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_ListEventsResponse{ListEventsResponse: response}})
				}()
			case *aop.ProtocolMessage_WatchEventsRequest:
				subscriptionCtx, subscriptionCancel := context.WithCancel(ctx)
				setSubscription(envelope.Id, subscriptionCancel)
				go func(subscriptionID string) {
					defer cancelSubscription(subscriptionID)
					err := sessions.WatchEvents(subscriptionCtx, payload.WatchEventsRequest, func(delivery *aop.EventDelivery) error {
						if delivery.GetEvent() == nil {
							return nil
						}
						return send(subscriptionID, delivery.GetCursor(), &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: delivery.Event}})
					})
					if err != nil && subscriptionCtx.Err() == nil {
						fail(subscriptionID, "WATCH_EVENTS_FAILED", err)
					}
				}(envelope.Id)
			case *aop.ProtocolMessage_CancelOperation:
				target := payload.CancelOperation.GetTargetId()
				cancelSubscription(target)
				removePTY(target, true)
			default:
				fail(envelope.Id, "UNSUPPORTED_MESSAGE", fmt.Errorf("unsupported AOP core message"))
			}

		case *types.CommandProtocolMessage:
			request := value.GetRequest()
			if request == nil {
				fail(envelope.Id, "UNSUPPORTED_MESSAGE", fmt.Errorf("unsupported AIScan command message"))
				return
			}
			go func() {
				operationID, err := s.ExecuteSessionCommand(request.SessionId, request.Line)
				if err != nil {
					fail(envelope.Id, "COMMAND_FAILED", err)
					return
				}
				_ = send(envelope.Id, "", &types.CommandProtocolMessage{Message: &types.CommandProtocolMessage_Receipt{Receipt: &types.CommandReceipt{OperationId: operationID, SessionId: request.SessionId, State: "running"}}})
			}()

		case *filepb.ProtocolMessage:
			request := value.GetUploadRequest()
			if request == nil {
				fail(envelope.Id, "UNSUPPORTED_MESSAGE", fmt.Errorf("only file upload is supported by the browser peer"))
				return
			}
			go func() {
				result, err := s.HandleFileUpload(ctx, request.SessionId, request.Filename, request.Data)
				if err != nil {
					fail(envelope.Id, "FILE_UPLOAD_FAILED", err)
					return
				}
				_ = send(envelope.Id, "", &filepb.ProtocolMessage{Message: &filepb.ProtocolMessage_Result{Result: result}})
			}()

		case *types.ScanProtocolMessage:
			request := value.GetWatchEventsRequest()
			if request == nil {
				fail(envelope.Id, "UNSUPPORTED_MESSAGE", fmt.Errorf("unsupported AIScan scan message"))
				return
			}
			subscriptionCtx, subscriptionCancel := context.WithCancel(ctx)
			setSubscription(envelope.Id, subscriptionCancel)
			go func(subscriptionID string) {
				defer cancelSubscription(subscriptionID)
				err := scans.WatchScanEvents(request, subscriptionCtx, func(event *types.ScanEvent) error {
					if event == nil {
						return nil
					}
					return send(subscriptionID, strconv.FormatUint(event.Sequence, 10), &types.ScanProtocolMessage{Message: &types.ScanProtocolMessage_Event{Event: event}})
				})
				if err != nil && subscriptionCtx.Err() == nil {
					fail(subscriptionID, "WATCH_SCAN_FAILED", err)
				}
			}(envelope.Id)

		case *ptypb.ProtocolMessage:
			frame := terminalcodec.FromProto(value)
			if frame.StreamID == "" {
				fail(envelope.Id, "INVALID_PTY", fmt.Errorf("PTY stream_id is required"))
				return
			}
			nodeID := ""
			switch payload := value.Message.(type) {
			case *ptypb.ProtocolMessage_Open:
				nodeID = payload.Open.NodeId
			case *ptypb.ProtocolMessage_List:
				nodeID = payload.List.NodeId
			}
			stateMu.Lock()
			route, routed := ptyRoutes[frame.StreamID]
			stateMu.Unlock()
			if nodeID == "" && routed {
				nodeID = route.nodeID
			}
			if nodeID == "" {
				fail(envelope.Id, "INVALID_PTY", fmt.Errorf("PTY node_id is required when opening a stream"))
				return
			}
			if !routed {
				events, online, unsubscribe := s.agents.subscribePTY(nodeID, frame.StreamID)
				stateMu.Lock()
				ptyRoutes[frame.StreamID] = browserPTYRoute{nodeID: nodeID, unsubscribe: unsubscribe}
				stateMu.Unlock()
				go func(streamID string, values <-chan pty.Frame) {
					for {
						select {
						case next, ok := <-values:
							if !ok {
								return
							}
							_ = send(streamID, "", terminalcodec.ToProto(next))
						case <-ctx.Done():
							return
						}
					}
				}(frame.StreamID, events)
				if !online {
					_ = send(frame.StreamID, "", terminalcodec.ToProto(pty.Frame{Type: pty.FrameDetached, StreamID: frame.StreamID}))
				}
			}
			if err := s.agents.sendAgentMessage(nodeID, generateID(), "", value); err != nil {
				fail(envelope.Id, "PTY_FORWARD_FAILED", err)
				removePTY(frame.StreamID, false)
				return
			}
			if frame.Type == pty.FrameDetach || frame.Type == pty.FrameClosed {
				removePTY(frame.StreamID, false)
			}

		default:
			fail(envelope.Id, "UNSUPPORTED_NAMESPACE", fmt.Errorf("unsupported browser AOP namespace"))
		}
	}

	handle(first)
	for {
		envelope, err := stream.Recv()
		if err != nil {
			return err
		}
		handle(envelope)
		select {
		case err := <-writeErr:
			return err
		default:
		}
	}
}
