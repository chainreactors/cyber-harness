# EventBus core

`core/eventbus` is the protocol-agnostic messaging primitive of Cyber Harness.
It provides typed publication, synchronous observation, bounded asynchronous
consumption, error isolation, flush and drain semantics.

The bus does not define an Event DTO, AOP envelope, sequence policy, sink or
serialization format. Extensions choose their payload type and own those
policies. For example, an AOP extension may publish `*aop.Event`, while an
output extension subscribes to that bus and writes JSONL directly from its
consumer callback. `Subscription.Flush` and `Subscription.Close` are the only
queue lifecycle APIs required by consumers.
