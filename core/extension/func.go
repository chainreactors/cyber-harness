package extension

import "context"

// Func installs and closes a contribution expressed by callbacks.
type Func struct {
	LoadFunc  func(*Context) error
	CloseFunc func(context.Context) error
}

func (f Func) Load(scope *Context) error {
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
