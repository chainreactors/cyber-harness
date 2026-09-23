package service

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/pkg/aopconn"
	"strconv"
	"sync"

	aop "github.com/chainreactors/cyber/aop"
	filepb "github.com/chainreactors/cyber/aop/file"
	ptypb "github.com/chainreactors/cyber/aop/pty"
	types "github.com/chainreactors/cyber/core/types"
	scanpb "github.com/chainreactors/cyber/pkg/web/scan"
	protobuf "google.golang.org/protobuf/proto"
)

type applicationPTYRoute struct {
	nodeID      string
	unsubscribe func()
}

func (s *Service) serveApplication(connection *aopconn.Connection, registerNamespaces func(*aop.NamespaceMux) error) error {
	if s == nil || s.api == nil || s.api.Sessions == nil || connection == nil {
		return fmt.Errorf("application AOP connection is unavailable")
	}
	ctx := connection.Context()
	var workers sync.WaitGroup
	defer func() { connection.Close(); workers.Wait() }()

	var stateMu sync.Mutex
	subscriptions := make(map[string]context.CancelFunc)
	ptyRoutes := make(map[string]applicationPTYRoute)

	send := func(replyTo, cursor string, message protobuf.Message) error {
		envelope, err := aop.Wrap(generateID(), replyTo, message)
		if err != nil {
			return err
		}
		envelope.DeliveryCursor = cursor
		return connection.Send(envelope)
	}
	fail := func(replyTo, code string, failure error) {
		if failure == nil {
			return
		}
		_ = send(replyTo, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_ProtocolError{ProtocolError: &aop.ProtocolError{Code: code, Message: failure.Error()}}})
	}
	setSubscription := func(id string, cancel context.CancelFunc) {
		stateMu.Lock()
		previous := subscriptions[id]
		subscriptions[id] = cancel
		stateMu.Unlock()
		if previous != nil {
			previous()
		}
	}
	cancelSubscription := func(id string) {
		stateMu.Lock()
		cancel := subscriptions[id]
		delete(subscriptions, id)
		stateMu.Unlock()
		if cancel != nil {
			cancel()
		}
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
		if detach && s.agents != nil {
			s.agents.ClosePTY(route.nodeID, streamID)
		}
	}
	defer func() {
		stateMu.Lock()
		cancels := make([]context.CancelFunc, 0, len(subscriptions))
		routes := make(map[string]applicationPTYRoute, len(ptyRoutes))
		for _, cancel := range subscriptions {
			cancels = append(cancels, cancel)
		}
		for streamID, route := range ptyRoutes {
			routes[streamID] = route
		}
		subscriptions = make(map[string]context.CancelFunc)
		ptyRoutes = make(map[string]applicationPTYRoute)
		stateMu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}
		for streamID, route := range routes {
			route.unsubscribe()
			if s.agents != nil {
				s.agents.ClosePTY(route.nodeID, streamID)
			}
		}
	}()

	handleCore := func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*aop.ProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected application core message %T", message)
		}
		if value.GetAgentHello() != nil {
			err := fmt.Errorf("AgentHello is only accepted by the node endpoint")
			fail(envelope.GetId(), "WRONG_ENDPOINT", err)
			connection.Close()
			return nil
		}
		sessions := s.api.Sessions
		switch payload := value.Message.(type) {
		case *aop.ProtocolMessage_OpenSessionRequest:
			workers.Add(1)
			go func() {
				defer workers.Done()
				response, err := sessions.OpenSession(ctx, envelope.Id, payload.OpenSessionRequest)
				if err != nil {
					fail(envelope.Id, "OPEN_SESSION_FAILED", err)
					return
				}
				_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionResponse{OpenSessionResponse: response}})
			}()
		case *aop.ProtocolMessage_RunTurnRequest:
			workers.Add(1)
			go func() {
				defer workers.Done()
				response, err := sessions.RunTurn(ctx, envelope.Id, payload.RunTurnRequest)
				if err != nil {
					fail(envelope.Id, "RUN_TURN_FAILED", err)
					return
				}
				_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnResponse{RunTurnResponse: response}})
			}()
		case *aop.ProtocolMessage_CancelTurnRequest:
			workers.Add(1)
			go func() {
				defer workers.Done()
				response, err := sessions.CancelTurn(ctx, envelope.Id, payload.CancelTurnRequest)
				if err != nil {
					fail(envelope.Id, "CANCEL_TURN_FAILED", err)
					return
				}
				_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelTurnResponse{CancelTurnResponse: response}})
			}()
		case *aop.ProtocolMessage_CloseSessionRequest:
			workers.Add(1)
			go func() {
				defer workers.Done()
				response, err := sessions.CloseSession(ctx, envelope.Id, payload.CloseSessionRequest)
				if err != nil {
					fail(envelope.Id, "CLOSE_SESSION_FAILED", err)
					return
				}
				_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionResponse{CloseSessionResponse: response}})
			}()
		case *aop.ProtocolMessage_ListEventsRequest:
			workers.Add(1)
			go func() {
				defer workers.Done()
				response, err := sessions.ListEvents(ctx, payload.ListEventsRequest)
				if err != nil {
					fail(envelope.Id, "LIST_EVENTS_FAILED", err)
					return
				}
				_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_ListEventsResponse{ListEventsResponse: response}})
			}()
		case *aop.ProtocolMessage_WatchEventsRequest:
			subscriptionCtx, cancel := context.WithCancel(ctx)
			setSubscription(envelope.Id, cancel)
			workers.Add(1)
			go func(subscriptionID string) {
				defer workers.Done()
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
		return nil
	}

	handleCommand := func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*types.CommandProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected application command message %T", message)
		}
		request := value.GetRequest()
		if request == nil {
			fail(envelope.Id, "UNSUPPORTED_MESSAGE", fmt.Errorf("unsupported Cyber command message"))
			return nil
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			operationID, err := s.ExecuteSessionCommand(request.SessionId, request.Line)
			if err != nil {
				fail(envelope.Id, "COMMAND_FAILED", err)
				return
			}
			_ = send(envelope.Id, "", &types.CommandProtocolMessage{Message: &types.CommandProtocolMessage_Receipt{Receipt: &types.CommandReceipt{OperationId: operationID, SessionId: request.SessionId, State: "running"}}})
		}()
		return nil
	}

	handleFile := func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*filepb.ProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected application file message %T", message)
		}
		request := value.GetUploadRequest()
		if request == nil {
			fail(envelope.Id, "UNSUPPORTED_MESSAGE", fmt.Errorf("only file upload is supported by the application endpoint"))
			return nil
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := s.Upload(ctx, request.SessionId, request.Filename, request.Data)
			if err != nil {
				fail(envelope.Id, "FILE_UPLOAD_FAILED", err)
				return
			}
			_ = send(envelope.Id, "", &filepb.ProtocolMessage{Message: &filepb.ProtocolMessage_Result{Result: result}})
		}()
		return nil
	}

	handleScan := func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*scanpb.ScanProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected application scan message %T", message)
		}
		request := value.GetWatchEventsRequest()
		if request == nil {
			fail(envelope.Id, "UNSUPPORTED_MESSAGE", fmt.Errorf("unsupported Cyber scan message"))
			return nil
		}
		subscriptionCtx, cancel := context.WithCancel(ctx)
		setSubscription(envelope.Id, cancel)
		workers.Add(1)
		go func(subscriptionID string) {
			defer workers.Done()
			defer cancelSubscription(subscriptionID)
			err := s.api.Scans.WatchScanEvents(request, subscriptionCtx, func(event *scanpb.ScanEvent) error {
				if event == nil {
					return nil
				}
				return send(subscriptionID, strconv.FormatUint(event.Sequence, 10), &scanpb.ScanProtocolMessage{Message: &scanpb.ScanProtocolMessage_Event{Event: event}})
			})
			if err != nil && subscriptionCtx.Err() == nil {
				fail(subscriptionID, "WATCH_SCAN_FAILED", err)
			}
		}(envelope.Id)
		return nil
	}

	handlePTY := func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*ptypb.ProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected application PTY message %T", message)
		}
		streamID := ptypb.StreamID(value)
		if streamID == "" {
			fail(envelope.Id, "INVALID_PTY", fmt.Errorf("PTY stream_id is required"))
			return nil
		}
		nodeID := ptypb.NodeID(value)
		stateMu.Lock()
		route, routed := ptyRoutes[streamID]
		stateMu.Unlock()
		if nodeID == "" && routed {
			nodeID = route.nodeID
		}
		if nodeID == "" {
			fail(envelope.Id, "INVALID_PTY", fmt.Errorf("PTY node_id is required when opening a stream"))
			return nil
		}
		if !routed {
			messages, online, unsubscribe := s.agents.SubscribePTY(nodeID, streamID)
			stateMu.Lock()
			ptyRoutes[streamID] = applicationPTYRoute{nodeID: nodeID, unsubscribe: unsubscribe}
			stateMu.Unlock()
			workers.Add(1)
			go func(streamID string, values <-chan *ptypb.ProtocolMessage) {
				defer workers.Done()
				for {
					select {
					case next, ok := <-values:
						if !ok {
							return
						}
						_ = send(streamID, "", next)
					case <-ctx.Done():
						return
					}
				}
			}(streamID, messages)
			if !online {
				_ = send(streamID, "", ptypb.NewDetached(streamID))
			}
		}
		if err := s.agents.ForwardPTY(nodeID, value); err != nil {
			fail(envelope.Id, "PTY_FORWARD_FAILED", err)
			removePTY(streamID, false)
			return nil
		}
		if ptypb.IsDetach(value) {
			removePTY(streamID, false)
		}
		return nil
	}

	mux := aop.NewNamespaceMux(ctx)
	defer mux.Close(context.Background())
	registrations := []struct {
		enabled   bool
		prototype protobuf.Message
		handler   aop.NamespaceHandler
	}{
		{enabled: true, prototype: &aop.ProtocolMessage{}, handler: handleCore},
		{enabled: true, prototype: &types.CommandProtocolMessage{}, handler: handleCommand},
		{enabled: true, prototype: &filepb.ProtocolMessage{}, handler: handleFile},
		{enabled: s.api.Scans != nil, prototype: &scanpb.ScanProtocolMessage{}, handler: handleScan},
		{enabled: s.agents != nil, prototype: &ptypb.ProtocolMessage{}, handler: handlePTY},
	}
	for _, registration := range registrations {
		if !registration.enabled {
			continue
		}
		if err := mux.Register(registration.prototype, registration.handler); err != nil {
			return fmt.Errorf("register application namespace: %w", err)
		}
	}
	if registerNamespaces != nil {
		if err := registerNamespaces(mux); err != nil {
			return fmt.Errorf("register application extension namespace: %w", err)
		}
	}
	dispatch := func(_ context.Context, envelope *aop.Envelope, sendEnvelope aop.SendFunc) error {
		handled, err := mux.Dispatch(envelope, sendEnvelope)
		if err != nil {
			fail(envelope.GetId(), "INVALID_PAYLOAD", err)
			return nil
		}
		if !handled {
			fail(envelope.GetId(), "UNSUPPORTED_NAMESPACE", fmt.Errorf("unsupported application AOP namespace"))
		}
		return nil
	}
	return connection.Run(dispatch)
}
