package api

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
	ptypb "github.com/chainreactors/aiscan/aop/pty"
	coreterminal "github.com/chainreactors/aiscan/core/terminal"
	types "github.com/chainreactors/aiscan/pkg/types"
	protobuf "google.golang.org/protobuf/proto"
)

// CommandExecutor runs "/" commands inside a chat session.
type CommandExecutor interface {
	ExecuteSessionCommand(sessionID, line string) (string, error)
}

// FileUploader stores an application-uploaded file through the owning agent.
type FileUploader interface {
	Upload(ctx context.Context, sessionID, filename string, data []byte) (*filepb.Result, error)
}

// PTYRouter bridges application PTY messages to the agent owning the terminal.
// It speaks generated protobuf only; frame conversion belongs to the
// mechanism layer implementing this interface.
type PTYRouter interface {
	SubscribePTY(nodeID, streamID string) (<-chan *ptypb.ProtocolMessage, bool, func())
	ForwardPTY(nodeID string, message *ptypb.ProtocolMessage) error
	ClosePTY(nodeID, streamID string)
}

// ApplicationConnection is the minimal mechanism surface required by the
// application business dispatcher. pkg/web owns the concrete Connection.
type ApplicationConnection interface {
	Context() context.Context
	Send(*aop.Envelope) error
	Run(*aop.Envelope, func(context.Context, *aop.Envelope, aop.SendFunc) error) error
}

// ApplicationBackends wires the application envelope business surface to its
// owning mechanisms.
type ApplicationBackends struct {
	Sessions *Sessions
	Scans    *Scans
	Commands CommandExecutor
	Files    FileUploader
	PTY      PTYRouter
	NewID    func() string
}

type applicationPTYRoute struct {
	nodeID      string
	unsubscribe func()
}

// ServeApplication serves one application connection over the AOP envelope
// surface. Connection owns stream concurrency; this function owns only the
// application business routes and connection-local subscriptions.
func ServeApplication(connection ApplicationConnection, first *aop.Envelope, backends *ApplicationBackends) error {
	if backends == nil || backends.Sessions == nil || backends.Scans == nil || backends.NewID == nil || connection == nil || first == nil {
		return fmt.Errorf("application AOP connection is unavailable")
	}
	ctx := connection.Context()

	var stateMu sync.Mutex
	subscriptions := make(map[string]context.CancelFunc)
	ptyRoutes := make(map[string]applicationPTYRoute)

	send := func(replyTo, cursor string, message protobuf.Message) error {
		envelope, wrapErr := aop.Wrap(backends.NewID(), replyTo, message)
		if wrapErr != nil {
			return wrapErr
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
	setSubscription := func(id string, subscriptionCancel context.CancelFunc) {
		stateMu.Lock()
		previous := subscriptions[id]
		subscriptions[id] = subscriptionCancel
		stateMu.Unlock()
		if previous != nil {
			previous()
		}
	}
	cancelSubscription := func(id string) {
		stateMu.Lock()
		subscriptionCancel := subscriptions[id]
		delete(subscriptions, id)
		stateMu.Unlock()
		if subscriptionCancel != nil {
			subscriptionCancel()
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
		if detach && backends.PTY != nil {
			backends.PTY.ClosePTY(route.nodeID, streamID)
		}
	}
	defer func() {
		stateMu.Lock()
		cancels := make([]context.CancelFunc, 0, len(subscriptions))
		routes := make(map[string]applicationPTYRoute, len(ptyRoutes))
		for _, subscriptionCancel := range subscriptions {
			cancels = append(cancels, subscriptionCancel)
		}
		for streamID, route := range ptyRoutes {
			routes[streamID] = route
		}
		subscriptions = make(map[string]context.CancelFunc)
		ptyRoutes = make(map[string]applicationPTYRoute)
		stateMu.Unlock()
		for _, subscriptionCancel := range cancels {
			subscriptionCancel()
		}
		for streamID, route := range routes {
			route.unsubscribe()
			if backends.PTY != nil {
				backends.PTY.ClosePTY(route.nodeID, streamID)
			}
		}
	}()

	handleCore := func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*aop.ProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected application core message %T", message)
		}
		sessions := backends.Sessions
		switch payload := value.Message.(type) {
		case *aop.ProtocolMessage_OpenSessionRequest:
			go func() {
				response, callErr := sessions.OpenSession(ctx, envelope.Id, payload.OpenSessionRequest)
				if callErr != nil {
					fail(envelope.Id, "OPEN_SESSION_FAILED", callErr)
					return
				}
				_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionResponse{OpenSessionResponse: response}})
			}()
		case *aop.ProtocolMessage_RunTurnRequest:
			go func() {
				response, callErr := sessions.RunTurn(ctx, envelope.Id, payload.RunTurnRequest)
				if callErr != nil {
					fail(envelope.Id, "RUN_TURN_FAILED", callErr)
					return
				}
				_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnResponse{RunTurnResponse: response}})
			}()
		case *aop.ProtocolMessage_CancelTurnRequest:
			go func() {
				response, callErr := sessions.CancelTurn(ctx, envelope.Id, payload.CancelTurnRequest)
				if callErr != nil {
					fail(envelope.Id, "CANCEL_TURN_FAILED", callErr)
					return
				}
				_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelTurnResponse{CancelTurnResponse: response}})
			}()
		case *aop.ProtocolMessage_CloseSessionRequest:
			go func() {
				response, callErr := sessions.CloseSession(ctx, envelope.Id, payload.CloseSessionRequest)
				if callErr != nil {
					fail(envelope.Id, "CLOSE_SESSION_FAILED", callErr)
					return
				}
				_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionResponse{CloseSessionResponse: response}})
			}()
		case *aop.ProtocolMessage_ListEventsRequest:
			go func() {
				response, callErr := sessions.ListEvents(ctx, payload.ListEventsRequest)
				if callErr != nil {
					fail(envelope.Id, "LIST_EVENTS_FAILED", callErr)
					return
				}
				_ = send(envelope.Id, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_ListEventsResponse{ListEventsResponse: response}})
			}()
		case *aop.ProtocolMessage_WatchEventsRequest:
			subscriptionCtx, subscriptionCancel := context.WithCancel(ctx)
			setSubscription(envelope.Id, subscriptionCancel)
			go func(subscriptionID string) {
				defer cancelSubscription(subscriptionID)
				watchErr := sessions.WatchEvents(subscriptionCtx, payload.WatchEventsRequest, func(delivery *aop.EventDelivery) error {
					if delivery.GetEvent() == nil {
						return nil
					}
					return send(subscriptionID, delivery.GetCursor(), &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: delivery.Event}})
				})
				if watchErr != nil && subscriptionCtx.Err() == nil {
					fail(subscriptionID, "WATCH_EVENTS_FAILED", watchErr)
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
		if request == nil || backends.Commands == nil {
			fail(envelope.Id, "UNSUPPORTED_MESSAGE", fmt.Errorf("unsupported AIScan command message"))
			return nil
		}
		go func() {
			operationID, callErr := backends.Commands.ExecuteSessionCommand(request.SessionId, request.Line)
			if callErr != nil {
				fail(envelope.Id, "COMMAND_FAILED", callErr)
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
		if request == nil || backends.Files == nil {
			fail(envelope.Id, "UNSUPPORTED_MESSAGE", fmt.Errorf("only file upload is supported by the application endpoint"))
			return nil
		}
		go func() {
			result, callErr := backends.Files.Upload(ctx, request.SessionId, request.Filename, request.Data)
			if callErr != nil {
				fail(envelope.Id, "FILE_UPLOAD_FAILED", callErr)
				return
			}
			_ = send(envelope.Id, "", &filepb.ProtocolMessage{Message: &filepb.ProtocolMessage_Result{Result: result}})
		}()
		return nil
	}

	handleScan := func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*types.ScanProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected application scan message %T", message)
		}
		request := value.GetWatchEventsRequest()
		if request == nil {
			fail(envelope.Id, "UNSUPPORTED_MESSAGE", fmt.Errorf("unsupported AIScan scan message"))
			return nil
		}
		subscriptionCtx, subscriptionCancel := context.WithCancel(ctx)
		setSubscription(envelope.Id, subscriptionCancel)
		go func(subscriptionID string) {
			defer cancelSubscription(subscriptionID)
			watchErr := backends.Scans.WatchScanEvents(request, subscriptionCtx, func(event *types.ScanEvent) error {
				if event == nil {
					return nil
				}
				return send(subscriptionID, strconv.FormatUint(event.Sequence, 10), &types.ScanProtocolMessage{Message: &types.ScanProtocolMessage_Event{Event: event}})
			})
			if watchErr != nil && subscriptionCtx.Err() == nil {
				fail(subscriptionID, "WATCH_SCAN_FAILED", watchErr)
			}
		}(envelope.Id)
		return nil
	}

	handlePTY := func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, ok := message.(*ptypb.ProtocolMessage)
		if !ok {
			return fmt.Errorf("unexpected application PTY message %T", message)
		}
		streamID := coreterminal.StreamID(value)
		if streamID == "" {
			fail(envelope.Id, "INVALID_PTY", fmt.Errorf("PTY stream_id is required"))
			return nil
		}
		if backends.PTY == nil {
			fail(envelope.Id, "UNSUPPORTED_MESSAGE", fmt.Errorf("PTY is unavailable"))
			return nil
		}
		nodeID := coreterminal.NodeID(value)
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
			messages, online, unsubscribe := backends.PTY.SubscribePTY(nodeID, streamID)
			stateMu.Lock()
			ptyRoutes[streamID] = applicationPTYRoute{nodeID: nodeID, unsubscribe: unsubscribe}
			stateMu.Unlock()
			go func(streamID string, values <-chan *ptypb.ProtocolMessage) {
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
				_ = send(streamID, "", coreterminal.NewDetached(streamID))
			}
		}
		if forwardErr := backends.PTY.ForwardPTY(nodeID, value); forwardErr != nil {
			fail(envelope.Id, "PTY_FORWARD_FAILED", forwardErr)
			removePTY(streamID, false)
			return nil
		}
		if coreterminal.IsDetach(value) {
			removePTY(streamID, false)
		}
		return nil
	}

	mux := aop.NewNamespaceMux()
	registrations := []struct {
		prototype protobuf.Message
		handler   aop.NamespaceHandler
	}{
		{prototype: &aop.ProtocolMessage{}, handler: handleCore},
		{prototype: &types.CommandProtocolMessage{}, handler: handleCommand},
		{prototype: &filepb.ProtocolMessage{}, handler: handleFile},
		{prototype: &types.ScanProtocolMessage{}, handler: handleScan},
		{prototype: &ptypb.ProtocolMessage{}, handler: handlePTY},
	}
	for _, registration := range registrations {
		if err := mux.Register(registration.prototype, registration.handler); err != nil {
			return fmt.Errorf("register application namespace: %w", err)
		}
	}
	dispatch := func(dispatchCtx context.Context, envelope *aop.Envelope, sendEnvelope aop.SendFunc) error {
		handled, dispatchErr := mux.Dispatch(dispatchCtx, envelope, sendEnvelope)
		if dispatchErr != nil {
			fail(envelope.GetId(), "INVALID_PAYLOAD", dispatchErr)
			return nil
		}
		if !handled {
			fail(envelope.GetId(), "UNSUPPORTED_NAMESPACE", fmt.Errorf("unsupported application AOP namespace"))
		}
		return nil
	}
	return connection.Run(first, dispatch)
}
