package aop

// EnvelopeStream is the transport boundary for AOP. Implementations only
// frame and carry protobuf Envelopes; application routing and lifecycle stay
// in the concrete Hub or AgentRuntime loop.
//
// A caller must use at most one Recv goroutine and one Send goroutine.
type EnvelopeStream interface {
	Recv() (*Envelope, error)
	Send(*Envelope) error
}
