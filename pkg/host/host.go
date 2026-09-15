// Package host carries AOP envelopes in-process and over stdio. Applications
// register their existing namespace handlers; host owns no agent or tools.
package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
)

// Host is one communication lifetime. It owns its connection's namespace mux and
// owns admission, cancellation and response serialization, never application
// sessions or tools. Construct a separate Host for each connection/embedding.
type Host struct {
	mux    *aop.NamespaceMux
	mu     sync.Mutex
	closed bool
	err    error
	active sync.WaitGroup
	sendMu sync.Mutex
}

func New(mux *aop.NamespaceMux) *Host {
	if mux == nil {
		panic("host requires a connection namespace mux")
	}
	return &Host{mux: mux}
}

// Context lets the stream owner interrupt its own blocking IO on disconnect.
func (h *Host) Context() context.Context { return h.mux.Context() }

// Handle is the inline entry point; Serve uses exactly the same dispatch.
// Asynchronous handlers receive the Host context and a guarded SendFunc. Their
// goroutines remain application-owned; Close cancels them and rejects late sends.
func (h *Host) Handle(envelope *aop.Envelope, send aop.SendFunc) error {
	if envelope == nil || send == nil {
		return fmt.Errorf("envelope and sender are required")
	}
	h.mu.Lock()
	if err := h.stateError(); err != nil {
		h.mu.Unlock()
		return err
	}
	h.active.Add(1)
	h.mu.Unlock()
	defer h.active.Done()
	reply := func(value *aop.Envelope) error { return h.Send(value, send) }
	handled, err := h.mux.Dispatch(envelope, reply)
	// Preserve IO failures; they must not become INVALID_PAYLOAD responses.
	if writeErr := h.Err(); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return reply(aop.Reply(envelope.Id, aop.NewProtocolError("INVALID_PAYLOAD", err.Error())))
	}
	if !handled {
		return reply(aop.Reply(envelope.Id, aop.NewProtocolError("UNSUPPORTED_MESSAGE", "unsupported protocol message")))
	}
	return nil
}

// Send uses the same close/error gate for replies and event subscriptions.
// Send callbacks must not reenter Handle, Send or Close on this Host.
func (h *Host) Send(envelope *aop.Envelope, send aop.SendFunc) error {
	if envelope == nil || send == nil {
		return fmt.Errorf("envelope and sender are required")
	}
	h.sendMu.Lock()
	defer h.sendMu.Unlock()
	h.mu.Lock()
	err := h.stateError()
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if err = send(envelope); err != nil {
		h.mu.Lock()
		h.err = err
		h.mu.Unlock()
		h.mux.Cancel()
	}
	return err
}

// Serve admits envelopes until EOF or failure. EOF leaves the Host open so
// the application can drain its admitted work before detaching subscriptions
// and calling Close. Stream ownership stays with the caller.
func (h *Host) Serve(stream aop.EnvelopeStream) error {
	if stream == nil {
		return fmt.Errorf("envelope stream is required")
	}
	for {
		h.mu.Lock()
		err := h.stateError()
		h.mu.Unlock()
		if err != nil {
			return err
		}
		envelope, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			if writeErr := h.Err(); writeErr != nil {
				return writeErr
			}
			return h.Context().Err()
		}
		if err != nil {
			return err
		}
		if err := h.Handle(envelope, stream.Send); err != nil {
			return err
		}
	}
}

// Close stops admission, cancels handlers and waits for active dispatches and
// sends. It does not close caller-owned IO or wait for handler-created goroutines.
// The owner must unblock its IO and wait for its application work separately.
// Call Close outside handlers and send callbacks; concurrent calls are safe.
func (h *Host) Close() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	_ = h.mux.Close(context.Background())
	h.active.Wait()
	// Acquiring the send lock after dispatch drains waits for a write that
	// started before Close; the empty critical section is the barrier.
	h.sendMu.Lock() //nolint:staticcheck // SA2001: the empty critical section is intentional
	h.sendMu.Unlock()
}

// Err retains the first write error, including asynchronous sends after EOF.
func (h *Host) Err() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

// stateError requires mu. Close and Handle synchronize here so Wait never
// races with admission of new dispatches.
func (h *Host) stateError() error {
	if h.err != nil {
		return h.err
	}
	if h.closed {
		return context.Canceled
	}
	return h.Context().Err()
}
