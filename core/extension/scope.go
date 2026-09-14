package extension

import "context"

// Scope belongs to one Extension installation in a Set. It separates the
// initialization budget from ongoing work, without owning registrations or
// resources. Extensions drain and release their resources in Close.
type Scope struct {
	init     context.Context
	lifetime context.Context
	cancel   context.CancelFunc
}

func newScope(init context.Context) *Scope {
	lifetime, cancel := context.WithCancel(context.Background())
	return &Scope{init: init, lifetime: lifetime, cancel: cancel}
}

// Init bounds Load only. Do not retain it for background work.
func (s *Scope) Init() context.Context { return s.init }

// Lifetime is canceled when the owning Set starts closing this extension.
// Cancellation is not a drain barrier; dependencies remain protected by Close.
func (s *Scope) Lifetime() context.Context { return s.lifetime }

func (s *Scope) stop() {
	if s != nil {
		s.cancel()
	}
}
