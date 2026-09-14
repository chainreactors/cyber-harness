package extension

import (
	"fmt"
	"reflect"
)

// Services is the per-Profile typed service table. It is populated by
// Providers during composition and becomes immutable after Seal. The untyped
// storage is private; callers can only access values through ServiceOf[T].
type Services struct {
	values    map[string]any
	contracts map[string]reflect.Type
	sealed    bool
}

func NewServices() *Services {
	return &Services{values: map[string]any{}, contracts: map[string]reflect.Type{}}
}

func (s *Services) Provide(contract Service, value any) error {
	if s == nil || s.sealed {
		return fmt.Errorf("services are sealed")
	}
	if contract.ID == "" || contract.Type == nil {
		return fmt.Errorf("invalid service contract")
	}
	if value == nil {
		return fmt.Errorf("service %s is nil", contract.ID)
	}
	t := reflect.TypeOf(value)
	if !t.AssignableTo(contract.Type) {
		return fmt.Errorf("service %s has type %s, want %s", contract.ID, t, contract.Type)
	}
	if _, exists := s.values[contract.ID]; exists {
		return fmt.Errorf("service %s already provided", contract.ID)
	}
	s.values[contract.ID], s.contracts[contract.ID] = value, contract.Type
	return nil
}

func (s *Services) Resolve(contract Service) (any, error) {
	if s == nil || !s.sealed {
		return nil, fmt.Errorf("services are not sealed")
	}
	value, ok := s.values[contract.ID]
	if !ok {
		return nil, fmt.Errorf("service %s is unavailable", contract.ID)
	}
	if s.contracts[contract.ID] != contract.Type {
		return nil, fmt.Errorf("service %s type mismatch", contract.ID)
	}
	return value, nil
}

func (s *Services) Seal() {
	if s != nil {
		s.sealed = true
	}
}
func (s *Services) Active() bool { return s != nil && s.sealed }

func Get[T any](s *Services, contract Service) (T, error) {
	var zero T
	value, err := s.Resolve(contract)
	if err != nil {
		return zero, err
	}
	result, ok := value.(T)
	if !ok {
		return zero, fmt.Errorf("service %s cannot be used as requested type", contract.ID)
	}
	return result, nil
}

func Optional[T any](s *Services, contract Service) (T, bool, error) {
	var zero T
	if s == nil || !s.sealed {
		return zero, false, fmt.Errorf("services are not sealed")
	}
	value, ok := s.values[contract.ID]
	if !ok {
		return zero, false, nil
	}
	result, ok := value.(T)
	if !ok {
		return zero, false, fmt.Errorf("service %s cannot be used as requested type", contract.ID)
	}
	return result, true, nil
}
