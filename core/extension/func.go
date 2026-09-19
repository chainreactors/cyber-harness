package extension

import "context"

// Func installs and closes a contribution expressed by callbacks.
type Func struct {
	LoadFunc  func(*Scope) error
	CloseFunc func(context.Context) error
}

func (f Func) Load(scope *Scope) error {
	if f.LoadFunc != nil {
		return f.LoadFunc(scope)
	}
	return nil
}
func (f Func) Close(ctx context.Context) error {
	if f.CloseFunc != nil {
		return f.CloseFunc(ctx)
	}
	return nil
}

// Provided owns value for the lifetime of the Set and publishes it as the
// capability keyed by T. A capability needs an owner whose lifetime bounds it,
// and a value the composition root already holds has no other owner to be --
// so this is that owner, and nothing more.
//
// Write the type argument explicitly. Inference registers the concrete type,
// while consumers borrow by the interface they named.
func Provided[T any](value T) Extension {
	return Func{LoadFunc: func(scope *Scope) error { return Provide[T](scope, value) }}
}
