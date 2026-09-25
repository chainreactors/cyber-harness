// Package proc is a registry of long-running, cancellable, observable units of
// work. A unit is not necessarily an OS process: an in-process function and a
// unit driven entirely by another subsystem are managed on the same terms as a
// command on a pseudo-terminal.
//
// A unit is described by an Attachment, the only thing that knows what kind of
// work it is. Everything else -- identity, state, the diagnostic ring buffer,
// deadlines, the stop ladder, events -- belongs to the Manager and is shared by
// every shape. There is exactly one supervisor, one stop ladder and one
// completion path.
//
// The Manager never learns what a unit does. Readiness and liveness are
// functions the attachment carries; the registry only knows "a function that
// eventually returns nil".
package proc
