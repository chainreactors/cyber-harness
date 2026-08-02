package web

import (
	"context"
	"errors"
	"fmt"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
)

const connectionOutboundBuffer = 128

var ErrConnectionClosed = errors.New("AOP connection closed")

type writeRequest struct {
	envelope *aop.Envelope
	result   chan error
}

// Connection is the root Web mechanism for one duplex EnvelopeStream. It owns
// exactly one reader and one FIFO writer; protocol and business state remain
// behind the Service abstraction.
type Connection struct {
	ctx    context.Context
	cancel context.CancelFunc
	stream aop.EnvelopeStream

	outbound chan writeRequest
	done     chan struct{}

	runMu sync.Mutex
	ran   bool

	stopOnce sync.Once
	errMu    sync.Mutex
	err      error
}

func NewConnection(parent context.Context, stream aop.EnvelopeStream) (*Connection, error) {
	if parent == nil {
		parent = context.Background()
	}
	if stream == nil {
		return nil, fmt.Errorf("AOP envelope stream is required")
	}
	ctx, cancel := context.WithCancel(parent)
	c := &Connection{
		ctx:      ctx,
		cancel:   cancel,
		stream:   stream,
		outbound: make(chan writeRequest, connectionOutboundBuffer),
		done:     make(chan struct{}),
	}
	go c.writeLoop()
	return c, nil
}

func (c *Connection) Context() context.Context {
	if c == nil || c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// Send serializes an envelope through the writer and waits for the underlying
// stream write. Concurrent callers are safe and cannot create parallel writes.
func (c *Connection) Send(envelope *aop.Envelope) error {
	if c == nil {
		return ErrConnectionClosed
	}
	if envelope == nil {
		return fmt.Errorf("AOP envelope is required")
	}
	request := writeRequest{envelope: envelope, result: make(chan error, 1)}
	select {
	case c.outbound <- request:
	case <-c.done:
		return c.terminalError()
	}
	select {
	case err := <-request.result:
		return err
	case <-c.done:
		select {
		case err := <-request.result:
			return err
		default:
			return c.terminalError()
		}
	}
}

// Run receives and dispatches envelopes until the stream, writer, handler, or
// context terminates. A non-nil first envelope is dispatched before Recv.
func (c *Connection) Run(first *aop.Envelope, handler func(context.Context, *aop.Envelope, aop.SendFunc) error) (runErr error) {
	if c == nil {
		return ErrConnectionClosed
	}
	if handler == nil {
		return fmt.Errorf("AOP envelope handler is required")
	}
	c.runMu.Lock()
	if c.ran {
		c.runMu.Unlock()
		return fmt.Errorf("AOP connection can only run once")
	}
	c.ran = true
	c.runMu.Unlock()

	defer func() { c.stop(runErr) }()
	if first != nil {
		if err := handler(c.ctx, first, c.Send); err != nil {
			return err
		}
	}

	received := make(chan *aop.Envelope)
	receiveErr := make(chan error, 1)
	go func() {
		for {
			envelope, err := c.stream.Recv()
			if err != nil {
				select {
				case receiveErr <- err:
				case <-c.done:
				}
				return
			}
			select {
			case received <- envelope:
			case <-c.done:
				return
			}
		}
	}()

	for {
		select {
		case envelope := <-received:
			if err := handler(c.ctx, envelope, c.Send); err != nil {
				return err
			}
		case err := <-receiveErr:
			return err
		case <-c.done:
			return c.terminalError()
		case <-c.ctx.Done():
			return c.ctx.Err()
		}
	}
}

func (c *Connection) Close() {
	if c != nil {
		c.stop(ErrConnectionClosed)
	}
}

func (c *Connection) writeLoop() {
	for {
		select {
		case request := <-c.outbound:
			err := c.stream.Send(request.envelope)
			request.result <- err
			if err != nil {
				c.stop(err)
				return
			}
		case <-c.ctx.Done():
			c.stop(c.ctx.Err())
			return
		case <-c.done:
			return
		}
	}
}

func (c *Connection) stop(err error) {
	c.stopOnce.Do(func() {
		if err == nil {
			err = ErrConnectionClosed
		}
		c.errMu.Lock()
		c.err = err
		c.errMu.Unlock()
		c.cancel()
		close(c.done)
	})
}

func (c *Connection) terminalError() error {
	if c == nil {
		return ErrConnectionClosed
	}
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.err != nil {
		return c.err
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	return ErrConnectionClosed
}
